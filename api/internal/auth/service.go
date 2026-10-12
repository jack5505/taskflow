package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/mail"
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
	Rotated   bool
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
	lock       lockout // вход: по email
	pwLock     lockout // смена пароля: неверный текущий, по id пользователя
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	baseURL    string
	mailer     mail.Sender
	jobs       *asynq.Client
	now        func() time.Time
}

func NewService(cfg *config.Config, db *gorm.DB, rdb *redis.Client) *Service {
	return &Service{
		db:         db,
		lock:       lockout{rdb: rdb, prefix: loginFailPrefix},
		pwLock:     lockout{rdb: rdb, prefix: pwchangeFailPrefix},
		secret:     []byte(cfg.JWTSecret),
		accessTTL:  cfg.JWTAccessTTL,
		refreshTTL: cfg.JWTRefreshTTL,
		baseURL:    cfg.BaseURL,
		now:        time.Now,
	}
}

// WithMailer задаёт, через что уходят письма со ссылками (очередь mail.Queue).
func (s *Service) WithMailer(m mail.Sender) *Service {
	s.mailer = m
	return s
}

// WithQueue подключает очередь заданий воркера (восстановление пароля в API).
func (s *Service) WithQueue(c *asynq.Client) *Service {
	s.jobs = c
	return s
}

// WithClock подменяет часы (тесты истечения токенов и блокировки).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Login проверяет блокировку, пароль и активность, выпускает пару токенов. Попытка
// засчитывается до проверки пароля (lockout.take): параллельные запросы не проверят больше
// maxFailures паролей за окно. Верный пароль счётчик сбрасывает.
func (s *Service) Login(ctx context.Context, email, password, userAgent string) (*Session, error) {
	n, locked, wait, err := s.lock.take(ctx, email)
	if err != nil {
		return nil, err
	}
	if locked {
		return nil, ErrLocked.With("retry_after_seconds", int(wait.Seconds()))
	}

	u, err := user.ByEmail(s.db.WithContext(ctx), email)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Хеш сравниваем и для несуществующего email: время ответа не выдаёт, есть ли пользователь.
		CheckPassword(dummyHash(), password)
		return nil, s.failed(ctx, email, n)
	case err != nil:
		s.release(ctx, s.lock, email) // пароль не проверен — попытка не в счёт
		return nil, fmt.Errorf("auth: find user: %w", err)
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, s.failed(ctx, email, n)
	}
	if !u.IsActive {
		// Пароль верный — не неудача: снимаем только эту попытку, накопленные неудачи остаются.
		s.release(ctx, s.lock, email)
		return nil, ErrUserInactive
	}
	if err := s.lock.reset(ctx, email); err != nil {
		return nil, err
	}
	return s.issue(ctx, u, userAgent)
}

// release возвращает попытку, взятую take; сбой Redis только в лог — попытка истечёт с окном.
func (s *Service) release(ctx context.Context, l lockout, id string) {
	if err := l.release(ctx, id); err != nil {
		slog.Warn("auth: lockout release", "err", err)
	}
}

// failed — неверный email или пароль: попытка уже засчитана take, с maxFailures-й неудачи
// блокировка держится ровно lockoutWindow.
func (s *Service) failed(ctx context.Context, email string, n int64) error {
	if err := s.lock.takeFailed(ctx, email, n); err != nil {
		return err
	}
	return ErrBadCredentials
}

// Refresh обменивает refresh-токен на новую пару: старый отзывается (ротация) и помечается
// rotated. Повторное использование уже отозванного ротацией токена значит, что он есть
// у двоих — у владельца и у того, кто его украл; тогда отзываются все сессии пользователя.
// Отзыв logout/«Выйти везде» rotated не ставит: его повтор — просто устаревшая сессия.
func (s *Service) Refresh(ctx context.Context, refreshToken, userAgent string) (*Session, error) {
	var session *Session
	var reusedBy uuid.UUID
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rt, err := lockRefreshToken(tx, hashToken(refreshToken))
		if err != nil {
			return err
		}
		if rt == nil {
			return ErrBadRefresh
		}
		now := s.now()
		if rt.RevokedAt != nil {
			if rt.Rotated {
				// Отзыв делаем отдельной транзакцией (ниже, после этой): эта закончится
				// возвратом ErrBadRefresh и откатится, вместе с ней откатился бы и отзыв.
				reusedBy = rt.UserID
			}
			return ErrBadRefresh
		}
		if !rt.ExpiresAt.After(now) {
			return ErrBadRefresh
		}
		u, err := user.ByID(tx, rt.UserID)
		if err != nil {
			return fmt.Errorf("auth: refresh user: %w", err)
		}
		if !u.IsActive {
			return ErrUserInactive
		}
		if err := tx.Model(&rt).Updates(map[string]any{"revoked_at": now, "rotated": true}).Error; err != nil {
			return fmt.Errorf("auth: revoke refresh: %w", err)
		}
		session, err = s.issueTx(tx, u, userAgent)
		return err
	})
	if reusedBy != uuid.Nil {
		if revokeErr := s.RevokeAll(ctx, reusedBy); revokeErr != nil {
			return nil, fmt.Errorf("auth: revoke all after reuse: %w", revokeErr)
		}
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

// Logout отзывает один refresh-токен; неизвестный токен — не ошибка.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return s.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("token_hash = ? AND revoked_at IS NULL", hashToken(refreshToken)).
		Update("revoked_at", s.now()).Error
}

// RevokeAll отзывает все сессии пользователя: «Выйти везде», смена пароля, деактивация (§9.1).
func (s *Service) RevokeAll(ctx context.Context, userID uuid.UUID) error {
	return s.RevokeAllTx(s.db.WithContext(ctx), userID)
}

// RevokeAllTx — RevokeAll внутри чужой транзакции (деактивация пользователя).
func (s *Service) RevokeAllTx(tx *gorm.DB, userID uuid.UUID) error {
	return tx.Model(&RefreshToken{}).
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
	u, err := user.ByID(s.db.WithContext(ctx), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNoToken
		}
		return nil, fmt.Errorf("auth: load user: %w", err)
	}
	if !u.IsActive {
		return nil, ErrUserInactive
	}
	return u, nil
}

// CleanupExpired удаляет просроченные и отозванные токены старше refreshTTL (воркер, раз в сутки).
// Заодно удаляет истёкшие ссылки установки пароля. Всё одной транзакцией.
func (s *Service) CleanupExpired(ctx context.Context) (int64, error) {
	now := s.now()
	var n int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("expires_at < ? OR revoked_at < ?", now, now.Add(-s.refreshTTL)).Delete(&RefreshToken{})
		if res.Error != nil {
			return res.Error
		}
		links := tx.Where("expires_at < ?", now).Delete(&PasswordToken{})
		if links.Error != nil {
			return links.Error
		}
		n = res.RowsAffected + links.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
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
// Считается лениво и один раз: bcrypt cost 12 на старте каждого процесса ни к чему.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("dummy-password-for-timing")
	if err != nil {
		panic(err)
	}
	return h
})
