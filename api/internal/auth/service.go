package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Ошибки домена (ТЗ §9.3). Сообщение про неверный email и пароль одно на оба случая:
// по нему нельзя узнать, есть ли такой пользователь.
var (
	ErrBadCredentials = apierr.Unauthorized("Неверный email или пароль")
	ErrUserInactive   = apierr.Unauthorized("Пользователь деактивирован")
	ErrLocked         = apierr.RateLimited("Слишком много неудачных попыток входа, подождите")
	ErrBadRefresh     = apierr.Unauthorized("Сессия истекла, войдите снова")
	ErrNoToken        = apierr.Unauthorized("Требуется авторизация")
)

// RefreshToken — строка таблицы refresh_tokens.
type RefreshToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `gorm:"type:uuid;not null"`
	TokenHash string    `gorm:"not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null"`
	RevokedAt *time.Time
	UserAgent string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

// Session — результат входа или обновления: access для заголовка, refresh для cookie.
type Session struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // секунды жизни access-токена
	User         *user.User
}

// Service — вход, обновление и отзыв сессий.
type Service struct {
	db         *gorm.DB
	lock       lockout
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

func NewService(cfg *config.Config, db *gorm.DB, rdb *redis.Client) *Service {
	return &Service{
		db:         db,
		lock:       lockout{rdb: rdb},
		secret:     []byte(cfg.JWTSecret),
		accessTTL:  cfg.JWTAccessTTL,
		refreshTTL: cfg.JWTRefreshTTL,
		now:        time.Now,
	}
}

// WithClock подменяет часы (тесты истечения токенов и блокировки).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Login проверяет блокировку, пароль и активность, выпускает пару токенов.
func (s *Service) Login(ctx context.Context, email, password, userAgent string) (*Session, error) {
	if locked, wait, err := s.lock.locked(ctx, email); err != nil {
		return nil, err
	} else if locked {
		return nil, ErrLocked.With("retry_after_seconds", int(wait.Seconds()))
	}

	var u user.User
	err := s.db.WithContext(ctx).Where("email = ?", email).First(&u).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Хеш сравниваем и для несуществующего email: время ответа не выдаёт, есть ли пользователь.
		CheckPassword(dummyHash, password)
		return nil, s.failed(ctx, email)
	case err != nil:
		return nil, fmt.Errorf("auth: find user: %w", err)
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, s.failed(ctx, email)
	}
	if !u.IsActive {
		return nil, ErrUserInactive
	}
	if err := s.lock.reset(ctx, email); err != nil {
		return nil, err
	}
	return s.issue(ctx, &u, userAgent)
}

func (s *Service) failed(ctx context.Context, email string) error {
	if err := s.lock.fail(ctx, email); err != nil {
		return err
	}
	return ErrBadCredentials
}

// Refresh обменивает refresh-токен на новую пару: старый отзывается (ротация),
// утёкший токен сработает один раз и выдаст себя при повторе.
func (s *Service) Refresh(ctx context.Context, refreshToken, userAgent string) (*Session, error) {
	var session *Session
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rt RefreshToken
		err := tx.Clauses(forUpdate).Where("token_hash = ?", hashToken(refreshToken)).First(&rt).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrBadRefresh
		}
		if err != nil {
			return fmt.Errorf("auth: find refresh: %w", err)
		}
		now := s.now()
		if rt.RevokedAt != nil || !rt.ExpiresAt.After(now) {
			return ErrBadRefresh
		}
		var u user.User
		if err := tx.First(&u, "id = ?", rt.UserID).Error; err != nil {
			return fmt.Errorf("auth: refresh user: %w", err)
		}
		if !u.IsActive {
			return ErrUserInactive
		}
		if err := tx.Model(&rt).Update("revoked_at", now).Error; err != nil {
			return fmt.Errorf("auth: revoke refresh: %w", err)
		}
		session, err = s.issueTx(tx, &u, userAgent)
		return err
	})
	return session, err
}

// Logout отзывает один refresh-токен; неизвестный токен — не ошибка.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return s.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("token_hash = ? AND revoked_at IS NULL", hashToken(refreshToken)).
		Update("revoked_at", s.now()).Error
}

// RevokeAll отзывает все сессии пользователя: «Выйти везде», смена пароля, деактивация (§9.1).
func (s *Service) RevokeAll(ctx context.Context, userID uuid.UUID) error {
	return s.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", s.now()).Error
}

// Authenticate проверяет access-токен и возвращает актуального пользователя из базы:
// деактивация действует сразу, не дожидаясь истечения токена.
func (s *Service) Authenticate(ctx context.Context, accessToken string) (*user.User, error) {
	id, err := parseAccess(s.secret, accessToken, s.now())
	if err != nil {
		return nil, ErrNoToken
	}
	var u user.User
	if err := s.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNoToken
		}
		return nil, fmt.Errorf("auth: load user: %w", err)
	}
	if !u.IsActive {
		return nil, ErrUserInactive
	}
	return &u, nil
}

// CleanupExpired удаляет просроченные и отозванные токены старше refreshTTL (воркер, раз в сутки).
func (s *Service) CleanupExpired(ctx context.Context) (int64, error) {
	res := s.db.WithContext(ctx).Where("expires_at < ? OR revoked_at < ?", s.now(), s.now().Add(-s.refreshTTL)).Delete(&RefreshToken{})
	return res.RowsAffected, res.Error
}

func (s *Service) issue(ctx context.Context, u *user.User, userAgent string) (*Session, error) {
	return s.issueTx(s.db.WithContext(ctx), u, userAgent)
}

func (s *Service) issueTx(tx *gorm.DB, u *user.User, userAgent string) (*Session, error) {
	now := s.now()
	raw, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	rt := RefreshToken{ID: uuid.New(), UserID: u.ID, TokenHash: hashToken(raw), ExpiresAt: now.Add(s.refreshTTL), UserAgent: userAgent, CreatedAt: now}
	if err := tx.Create(&rt).Error; err != nil {
		return nil, fmt.Errorf("auth: save refresh: %w", err)
	}
	access, err := signAccess(s.secret, u.ID, now, s.accessTTL)
	if err != nil {
		return nil, fmt.Errorf("auth: sign access: %w", err)
	}
	return &Session{AccessToken: access, RefreshToken: raw, ExpiresIn: int(s.accessTTL.Seconds()), User: u}, nil
}

// dummyHash — хеш для сравнения при неизвестном email (постоянное время ответа).
var dummyHash = func() string {
	h, _ := HashPassword("dummy-password-for-timing")
	return h
}()
