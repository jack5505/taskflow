// Package users — управление пользователями администратором системы (ТЗ §3.1, §9.2):
// список, создание с письмом для установки пароля, изменение и деактивация.
package users

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/user"
)

var (
	ErrNotFound    = apierr.NotFound("Пользователь не найден")
	ErrLastManager = apierr.Conflict(apierr.CodeLastManager,
		"Пользователь — единственный менеджер проекта: сначала назначьте другого менеджера")
	ErrForbiddenList = apierr.Forbidden("Список пользователей доступен администратору системы и менеджерам проектов")
)

func fieldError(field, reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: field, Reason: reason}})
}

// Service — правила §3.1 для пользователей; HTTP-обработчики и (позже) бот вызывают его.
type Service struct {
	db   *gorm.DB
	auth *auth.Service
}

func NewService(db *gorm.DB, authSvc *auth.Service) *Service {
	return &Service{db: db, auth: authSvc}
}

// CanList — §9.2: список видят администратор системы и менеджеры (для добавления в проект).
func (s *Service) CanList(ctx context.Context, u *user.User) (bool, error) {
	if u.IsSystemAdmin {
		return true, nil
	}
	var ok bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS (
		SELECT 1 FROM project_members m JOIN projects p ON p.id = m.project_id
		WHERE m.user_id = ? AND m.role = 'manager' AND p.deleted_at IS NULL)`, u.ID).Scan(&ok).Error
	if err != nil {
		return false, fmt.Errorf("users: manager check: %w", err)
	}
	return ok, nil
}

// List — все пользователи по имени.
func (s *Service) List(ctx context.Context) ([]user.User, error) {
	var list []user.User
	if err := s.db.WithContext(ctx).Order("display_name, email").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("users: list: %w", err)
	}
	return list, nil
}

type CreateInput struct {
	Email         string
	DisplayName   string
	Timezone      string
	IsSystemAdmin bool
}

// Create заводит пользователя без пароля и ставит письмо со ссылкой установки пароля (§7.1).
// Задание письма ставится до коммита: недоступна очередь — пользователь не создаётся.
func (s *Service) Create(ctx context.Context, in CreateInput) (*user.User, error) {
	name, err := displayName(in.DisplayName)
	if err != nil {
		return nil, err
	}
	tz := in.Timezone
	if tz == "" {
		tz = model.DefaultTimezone
	}
	if err := checkTimezone(tz); err != nil {
		return nil, err
	}
	u := user.New(in.Email, "", name)
	u.Timezone = tz
	u.IsSystemAdmin = in.IsSystemAdmin

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "email"}}, DoNothing: true}).Create(u)
		if res.Error != nil {
			return fmt.Errorf("users: create: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fieldError("email", "уже используется")
		}
		return s.auth.EnqueueInvite(ctx, u.ID)
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateInput — изменяемые поля; nil — не менять.
type UpdateInput struct {
	DisplayName   *string
	Timezone      *string
	IsSystemAdmin *bool
	IsActive      *bool
}

// Update меняет пользователя; IsActive=false — деактивация по §3.1.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*user.User, error) {
	updates := map[string]any{}
	if in.DisplayName != nil {
		name, err := displayName(*in.DisplayName)
		if err != nil {
			return nil, err
		}
		updates["display_name"] = name
	}
	if in.Timezone != nil {
		if err := checkTimezone(*in.Timezone); err != nil {
			return nil, err
		}
		updates["timezone"] = *in.Timezone
	}
	if in.IsSystemAdmin != nil {
		updates["is_system_admin"] = *in.IsSystemAdmin
	}

	var u user.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Raw(`SELECT * FROM users WHERE id = ? FOR UPDATE`, id).Scan(&u)
		if res.Error != nil {
			return fmt.Errorf("users: find: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if in.IsActive != nil && *in.IsActive != u.IsActive {
			if *in.IsActive {
				updates["is_active"] = true
			} else if err := s.deactivateTx(tx, &u); err != nil {
				return err
			}
		}
		if len(updates) > 0 {
			if err := tx.Model(&u).Updates(updates).Error; err != nil {
				return fmt.Errorf("users: update: %w", err)
			}
		}
		return tx.First(&u, "id = ?", id).Error
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// deactivateTx — деактивация §3.1 в транзакции обновления: проверка «единственного менеджера»,
// снятие привязки Telegram, отзыв всех сессий (§9.1), событие user.deactivated (§9.4).
// Остановка таймера, освобождение задач и уведомление менеджеров добавят задачи #33, #20 и #13.
func (s *Service) deactivateTx(tx *gorm.DB, u *user.User) error {
	// Блокируем проекты, где он менеджер: параллельная деактивация второго менеджера того же
	// проекта дождётся этой транзакции и увидит её результат.
	if err := tx.Exec(`SELECT p.id FROM projects p JOIN project_members m ON m.project_id = p.id
		WHERE m.user_id = ? AND m.role = 'manager' ORDER BY p.id FOR UPDATE OF p`, u.ID).Error; err != nil {
		return fmt.Errorf("users: lock projects: %w", err)
	}
	var keys []string
	if err := tx.Raw(`SELECT p.key FROM project_members m JOIN projects p ON p.id = m.project_id
		WHERE m.user_id = ? AND m.role = 'manager' AND p.deleted_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM project_members o JOIN users ou ON ou.id = o.user_id
			WHERE o.project_id = m.project_id AND o.role = 'manager' AND o.user_id <> m.user_id AND ou.is_active)
		ORDER BY p.key`, u.ID).Scan(&keys).Error; err != nil {
		return fmt.Errorf("users: last manager check: %w", err)
	}
	if len(keys) > 0 {
		return ErrLastManager.With("projects", keys)
	}
	if err := tx.Model(u).Updates(map[string]any{
		"is_active": false, "telegram_user_id": nil, "telegram_username": nil,
	}).Error; err != nil {
		return fmt.Errorf("users: deactivate: %w", err)
	}
	if err := s.auth.RevokeAllTx(tx, u.ID); err != nil {
		return fmt.Errorf("users: revoke sessions: %w", err)
	}
	return events.Publish(tx, events.UserDeactivated, map[string]any{"user_id": u.ID})
}

func displayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 {
		return "", fieldError("display_name", "от 1 до 80 символов")
	}
	return name, nil
}

func checkTimezone(tz string) error {
	if _, err := time.LoadLocation(tz); err != nil || tz == "" || tz == "Local" {
		return fieldError("timezone", "неизвестный часовой пояс IANA")
	}
	return nil
}
