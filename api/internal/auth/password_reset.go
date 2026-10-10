package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/user"
)

// passwordLinkTTL — ТЗ §7.1: ссылка установки и восстановления пароля действует 24 часа.
const passwordLinkTTL = 24 * time.Hour

// ErrBadPasswordToken — ссылка не найдена, уже использована или истекла.
var ErrBadPasswordToken = apierr.BadRequest(apierr.CodeValidation, "Ссылка недействительна или устарела, запросите новую").
	With("fields", []apierr.FieldError{{Field: "token", Reason: "ссылка недействительна или устарела"}})

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

// RequestPasswordReset отправляет письмо со ссылкой восстановления (ТЗ §7.1, §9.1).
// Для неизвестного или деактивированного email ничего не делает и ошибки не возвращает:
// ответ не должен выдавать, есть ли такой пользователь.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
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
	link, err := s.passwordLink(ctx, u.ID)
	if err != nil {
		return err
	}
	m, err := mail.PasswordReset(u.Email, u.DisplayName, link)
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, m)
}

// passwordLink выпускает одноразовую ссылку на 24 часа. Прежние неиспользованные ссылки
// пользователя гасятся: действует только последняя из писем.
func (s *Service) passwordLink(ctx context.Context, userID uuid.UUID) (string, error) {
	raw, err := newRefreshToken()
	if err != nil {
		return "", err
	}
	now := s.now()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND used_at IS NULL", userID).Delete(&PasswordToken{}).Error; err != nil {
			return fmt.Errorf("auth: drop old password links: %w", err)
		}
		pt := PasswordToken{ID: uuid.New(), UserID: userID, TokenHash: hashToken(raw), ExpiresAt: now.Add(passwordLinkTTL), CreatedAt: now}
		if err := tx.Create(&pt).Error; err != nil {
			return fmt.Errorf("auth: save password link: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s.baseURL, "/") + "/password-set?token=" + raw, nil
}

// SetPassword задаёт пароль по токену из ссылки. Токен гасится, все сессии пользователя
// отзываются (§9.1: смена пароля), счётчик неудачных входов сбрасывается.
func (s *Service) SetPassword(ctx context.Context, token, password string) error {
	hash, err := HashPassword(password)
	switch {
	case errors.Is(err, ErrPasswordTooShort):
		return passwordFieldError("не короче 10 символов")
	case errors.Is(err, ErrPasswordTooLong):
		return passwordFieldError("не длиннее 72 байт")
	case err != nil:
		return fmt.Errorf("auth: hash password: %w", err)
	}

	var email string
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		if err := tx.Model(&u).Update("password_hash", hash).Error; err != nil {
			return fmt.Errorf("auth: save password: %w", err)
		}
		if err := tx.Model(&pt).Update("used_at", now).Error; err != nil {
			return fmt.Errorf("auth: use password link: %w", err)
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
