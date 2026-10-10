package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/user"
)

const (
	// passwordLinkTTL — ТЗ §7.1: ссылка установки и восстановления пароля действует 24 часа.
	passwordLinkTTL = 24 * time.Hour
	// resetCooldown — не чаще одного письма восстановления на email за это время: защита
	// почтового ящика и квоты SMTP от флуда через открытый эндпоинт.
	resetCooldown = time.Minute
	// TaskPasswordReset — задание воркера: найти пользователя, выпустить ссылку, поставить письмо.
	TaskPasswordReset = "auth:password_reset"
	resetMaxRetry     = 10
)

var (
	// ErrBadPasswordToken — ссылка не найдена, уже использована или истекла.
	ErrBadPasswordToken = apierr.BadRequest(apierr.CodeValidation, "Ссылка недействительна или устарела, запросите новую").
				With("fields", []apierr.FieldError{{Field: "token", Reason: "ссылка недействительна или устарела"}})
	errNoQueue  = errors.New("auth: очередь заданий не подключена (WithQueue)")
	errNoMailer = errors.New("auth: отправка писем не подключена (WithMailer)")
)

// PasswordToken — строка таблицы password_tokens.
type PasswordToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `gorm:"type:uuid;not null"`
	TokenHash string    `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedAt time.Time `gorm:"not null"`
}

func (PasswordToken) TableName() string { return "password_tokens" }

type resetPayload struct {
	Email string `json:"email"`
}

// RequestPasswordReset — POST /auth/password-reset (ТЗ §7.1, §9.1). Для любого email делает
// одно и то же — ставит задание воркеру, поэтому ни время ответа, ни ошибка при недоступном
// Redis не выдают, есть ли такой пользователь. Пользователя ищет и письмо ставит воркер.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	if s.jobs == nil {
		return errNoQueue
	}
	key := "pwreset:cooldown:" + strings.ToLower(email)
	first, err := s.lock.rdb.SetNX(ctx, key, 1, resetCooldown).Result()
	if err != nil {
		return fmt.Errorf("auth: reset cooldown: %w", err)
	}
	if !first {
		return nil
	}
	payload, err := json.Marshal(resetPayload{Email: email})
	if err != nil {
		return err
	}
	_, err = s.jobs.EnqueueContext(ctx, asynq.NewTask(TaskPasswordReset, payload),
		asynq.Queue(queue.QueueDefault), asynq.MaxRetry(resetMaxRetry), asynq.Timeout(time.Minute))
	if err != nil {
		_ = s.lock.rdb.Del(ctx, key).Err() // запрос не принят — пусть пользователь сможет повторить сразу
		return fmt.Errorf("auth: enqueue password reset: %w", err)
	}
	return nil
}

// PasswordResetHandler — обработчик задания TaskPasswordReset в воркере.
func PasswordResetHandler(s *Service) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p resetPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("auth: reset payload: %w: %w", err, asynq.SkipRetry)
		}
		return s.SendPasswordReset(ctx, p.Email)
	}
}

// SendPasswordReset выпускает ссылку восстановления и ставит письмо в очередь.
// Неизвестный и деактивированный email молча пропускаются.
func (s *Service) SendPasswordReset(ctx context.Context, email string) error {
	if s.mailer == nil {
		return errNoMailer
	}
	var u user.User
	err := s.db.WithContext(ctx).First(&u, "email = ?", email).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth: find user: %w", err)
	}
	if !u.IsActive {
		return nil
	}
	return s.sendLink(ctx, &u, mail.PasswordReset)
}

// passwordLink выпускает одноразовую ссылку на 24 часа. Прежние ссылки не гасятся —
// иначе любой мог бы запросами восстановления «съедать» ссылку из уже полученного письма;
// все неиспользованные ссылки гасятся при успешной установке пароля.
func (s *Service) passwordLink(ctx context.Context, userID uuid.UUID) (string, error) {
	raw, err := newRefreshToken()
	if err != nil {
		return "", err
	}
	now := s.now()
	pt := PasswordToken{ID: uuid.New(), UserID: userID, TokenHash: hashToken(raw), ExpiresAt: now.Add(passwordLinkTTL), CreatedAt: now}
	if err := s.db.WithContext(ctx).Create(&pt).Error; err != nil {
		return "", fmt.Errorf("auth: save password link: %w", err)
	}
	return strings.TrimRight(s.baseURL, "/") + "/password-set?token=" + raw, nil
}

// SetPassword задаёт пароль по токену из ссылки. Гасятся все неиспользованные ссылки
// пользователя, отзываются все сессии (§9.1: смена пароля), сбрасывается счётчик неудачных входов.
// bcrypt считается только после проверки токена: открытый эндпоинт не должен тратить CPU на мусор.
func (s *Service) SetPassword(ctx context.Context, token, password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return passwordFieldError("не короче 10 символов")
	}
	if len(password) > MaxPasswordBytes {
		return passwordFieldError("не длиннее 72 байт")
	}

	var email string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pt PasswordToken
		res := tx.Raw(`SELECT id, user_id, token_hash, expires_at, used_at, created_at
			FROM password_tokens WHERE token_hash = ? FOR UPDATE`, hashToken(token)).Scan(&pt)
		if res.Error != nil {
			return fmt.Errorf("auth: find password link: %w", res.Error)
		}
		now := s.now()
		if res.RowsAffected == 0 || pt.UsedAt != nil || !pt.ExpiresAt.After(now) {
			return ErrBadPasswordToken
		}
		var u user.User
		if err := tx.First(&u, "id = ?", pt.UserID).Error; err != nil {
			return fmt.Errorf("auth: password link user: %w", err)
		}
		if !u.IsActive {
			return ErrBadPasswordToken
		}
		hash, err := HashPassword(password)
		if err != nil {
			return fmt.Errorf("auth: hash password: %w", err)
		}
		if err := tx.Model(&u).Update("password_hash", hash).Error; err != nil {
			return fmt.Errorf("auth: save password: %w", err)
		}
		if err := tx.Model(&PasswordToken{}).Where("user_id = ? AND used_at IS NULL", u.ID).
			Update("used_at", now).Error; err != nil {
			return fmt.Errorf("auth: use password links: %w", err)
		}
		if err := tx.Model(&RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", u.ID).
			Update("revoked_at", now).Error; err != nil {
			return fmt.Errorf("auth: revoke sessions: %w", err)
		}
		email = u.Email
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.lock.reset(ctx, email); err != nil {
		// Пароль уже сменён; блокировка истечёт сама.
		slog.Warn("auth: lockout reset after password set", "err", err)
	}
	return nil
}

func passwordFieldError(reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: "password", Reason: reason}})
}
