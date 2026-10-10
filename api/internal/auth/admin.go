package auth

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Результат EnsureSystemAdmin.
type AdminResult int

const (
	AdminCreated  AdminResult = iota // новый пользователь
	AdminPromoted                    // был обычным пользователем, стал администратором
	AdminExisted                     // уже был администратором
)

var (
	ErrBadEmail       = errors.New("некорректный email")
	ErrBadDisplayName = errors.New("имя должно быть от 1 до 80 символов")
	ErrAdminInactive  = errors.New("пользователь с таким email деактивирован")
)

// EnsureSystemAdmin — первый администратор системы (ТЗ §7.1, команда `taskflow admin create`).
// Повторный вызов с тем же email дубликата не создаёт: существующий пользователь
// становится администратором. Пароля у нового пользователя нет — его задают по ссылке.
func (s *Service) EnsureSystemAdmin(ctx context.Context, email, displayName string) (*user.User, AdminResult, error) {
	addr, err := netmail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return nil, 0, ErrBadEmail
	}
	if displayName == "" {
		displayName = email[:strings.IndexByte(email, '@')]
	}
	displayName = strings.TrimSpace(displayName)
	if n := utf8.RuneCountInString(displayName); n < 1 || n > 80 {
		return nil, 0, ErrBadDisplayName
	}

	var u user.User
	result := AdminExisted
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		nu := user.New(email, "", displayName) // пустой хеш не совпадёт ни с одним паролем
		nu.IsSystemAdmin = true
		res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "email"}}, DoNothing: true}).Create(nu)
		if res.Error != nil {
			return fmt.Errorf("auth: create admin: %w", res.Error)
		}
		if res.RowsAffected == 1 {
			u, result = *nu, AdminCreated
			return nil
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "email = ?", email).Error; err != nil {
			return fmt.Errorf("auth: find admin: %w", err)
		}
		if !u.IsActive {
			return ErrAdminInactive
		}
		if !u.IsSystemAdmin {
			if err := tx.Model(&u).Update("is_system_admin", true).Error; err != nil {
				return fmt.Errorf("auth: promote admin: %w", err)
			}
			result = AdminPromoted
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return &u, result, nil
}

// PasswordSetLink выпускает ссылку установки пароля (24 часа, одноразовая) — для вывода в консоль.
func (s *Service) PasswordSetLink(ctx context.Context, u *user.User) (string, error) {
	return s.passwordLink(ctx, u.ID)
}

// SendPasswordSetLink отправляет пользователю письмо-приглашение со ссылкой установки пароля.
func (s *Service) SendPasswordSetLink(ctx context.Context, u *user.User) error {
	link, err := s.passwordLink(ctx, u.ID)
	if err != nil {
		return err
	}
	m, err := mail.PasswordSet(u.Email, u.DisplayName, link)
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, m)
}
