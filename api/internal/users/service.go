// Package users — управление пользователями администратором системы (ТЗ §3.1, §9.2):
// список, создание с письмом для установки пароля, изменение и деактивация.
package users

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
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
	now  func() time.Time
}

func NewService(db *gorm.DB, authSvc *auth.Service) *Service {
	return &Service{db: db, auth: authSvc, now: time.Now}
}

// CanList — §9.2: список видят администратор системы и менеджеры (для добавления в проект).
func (s *Service) CanList(ctx context.Context, u *user.User) (bool, error) {
	if u.IsSystemAdmin {
		return true, nil
	}
	return project.ManagesAny(s.db.WithContext(ctx), u.ID)
}

// ListAll — все пользователи по имени (администратор системы).
func (s *Service) ListAll(ctx context.Context) ([]user.User, error) {
	return user.All(s.db.WithContext(ctx))
}

// ListBrief — активные пользователи без служебных полей: менеджеру для добавления в проект.
func (s *Service) ListBrief(ctx context.Context) ([]user.Brief, error) {
	return user.ActiveBriefs(s.db.WithContext(ctx))
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
		created, err := user.CreateNew(tx, u)
		if err != nil {
			return err
		}
		if !created {
			return fieldError("email", "уже используется")
		}
		if err := audit.Write(tx, audit.Record{Action: audit.UserCreated, EntityType: "user", EntityID: u.ID.String(),
			New: map[string]any{"email": u.Email, "display_name": u.DisplayName, "timezone": u.Timezone, "is_system_admin": u.IsSystemAdmin}}); err != nil {
			return err
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

	var u *user.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Снятие админа или деактивация может оставить систему без администратора: сначала
		// блокируем всех активных администраторов (в одном порядке, до строки пользователя —
		// иначе две такие транзакции навстречу друг другу встанут в deadlock).
		var admins []uuid.UUID
		var err error
		if (in.IsActive != nil && !*in.IsActive) || (in.IsSystemAdmin != nil && !*in.IsSystemAdmin) {
			if admins, err = user.LockActiveAdmins(tx); err != nil {
				return err
			}
		}
		// FOR NO KEY UPDATE, а не FOR UPDATE: проверки внешних ключей (история, комментарии со
		// ссылкой на пользователя) берут FOR KEY SHARE и с ним не конфликтуют — иначе
		// смена статуса задачи этого пользователя и его деактивация встали бы в deadlock.
		if u, err = user.LockNoKeyUpdate(tx, id); err != nil {
			return err
		}
		if u == nil {
			return ErrNotFound
		}
		if u.IsSystemAdmin && u.IsActive && len(admins) == 1 {
			if in.IsActive != nil && !*in.IsActive {
				return fieldError("is_active", "это последний активный администратор системы")
			}
			if in.IsSystemAdmin != nil && !*in.IsSystemAdmin {
				return fieldError("is_system_admin", "это последний активный администратор системы")
			}
		}
		before := *u
		if in.IsActive != nil && *in.IsActive != u.IsActive {
			if *in.IsActive {
				updates["is_active"] = true
				if err := audit.Write(tx, audit.Record{Action: audit.UserActivated, EntityType: "user", EntityID: u.ID.String()}); err != nil {
					return err
				}
			} else if err := s.deactivateTx(tx, u); err != nil {
				return err
			}
		}
		if old, nw := changed(&before, updates); len(nw) > 0 {
			if err := audit.Write(tx, audit.Record{Action: audit.UserUpdated, EntityType: "user", EntityID: u.ID.String(), Old: old, New: nw}); err != nil {
				return err
			}
		}
		return user.Update(tx, u, updates)
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// deactivateTx — деактивация §3.1 в транзакции обновления: проверка «единственного менеджера»
// (в том числе удалённых проектов — после восстановления у проекта должен быть менеджер),
// остановка таймера с записью времени (§6.4), снятие привязки Telegram, отзыв всех сессий (§9.1),
// событие user.deactivated (§9.4).
func (s *Service) deactivateTx(tx *gorm.DB, u *user.User) error {
	// §3.1: открытые задачи освобождаются. Задачи блокируются раньше проектов — тот же порядок
	// «задача → проект», что у смены статуса, иначе встречные транзакции дали бы deadlock.
	released, err := task.ReleaseOnDeactivation(tx, u.ID)
	if err != nil {
		return err
	}
	// Таймер идёт только на его задаче в работе — она уже заблокирована освобождением выше.
	if err := task.StopUserTimer(tx, u.ID, s.now()); err != nil {
		return err
	}
	// Блокируем проекты, где он менеджер: параллельная деактивация второго менеджера того же
	// проекта дождётся этой транзакции и увидит её результат.
	managed, err := project.LockManagedBy(tx, u.ID)
	if err != nil {
		return err
	}
	keys, err := lastManagerOf(tx, u.ID, managed)
	if err != nil {
		return err
	}
	if len(keys) > 0 {
		return ErrLastManager.With("projects", keys)
	}
	if err := user.Deactivate(tx, u); err != nil {
		return err
	}
	if err := s.auth.RevokeAllTx(tx, u.ID); err != nil {
		return fmt.Errorf("users: revoke sessions: %w", err)
	}
	if err := audit.Write(tx, audit.Record{Action: audit.UserDeactivated, EntityType: "user", EntityID: u.ID.String()}); err != nil {
		return err
	}
	by, _ := audit.From(tx.Statement.Context)
	return events.Publish(tx, events.UserDeactivated, map[string]any{"user_id": u.ID, "released_tasks": released, "by": by})
}

// lastManagerOf — ключи проектов из managed (userID в них менеджер), где других активных
// менеджеров нет, по ключу.
func lastManagerOf(tx *gorm.DB, userID uuid.UUID, managed []project.Brief) ([]string, error) {
	if len(managed) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(managed))
	for i, p := range managed {
		ids[i] = p.ID
	}
	managers, err := project.Managers(tx, ids)
	if err != nil {
		return nil, err
	}
	var others []uuid.UUID
	for _, m := range managers {
		if m.UserID != userID {
			others = append(others, m.UserID)
		}
	}
	active, err := user.ActiveTimezones(tx, others)
	if err != nil {
		return nil, err
	}
	covered := map[uuid.UUID]bool{}
	for _, m := range managers {
		if _, ok := active[m.UserID]; ok && m.UserID != userID {
			covered[m.ProjectID] = true
		}
	}
	var keys []string
	for _, p := range managed {
		if !covered[p.ID] {
			keys = append(keys, p.Key)
		}
	}
	slices.Sort(keys)
	return keys, nil
}

// changed — значения полей пользователя до и после для журнала; только реально меняющиеся.
func changed(u *user.User, updates map[string]any) (old, nw map[string]any) {
	return audit.Diff(map[string]any{"display_name": u.DisplayName, "timezone": u.Timezone, "is_system_admin": u.IsSystemAdmin}, updates)
}

func displayName(name string) (string, error) {
	name, ok := user.NormalizeDisplayName(name)
	if !ok {
		return "", fieldError("display_name", "от 1 до 80 символов")
	}
	return name, nil
}

func checkTimezone(tz string) error {
	if !model.ValidTimezone(tz) {
		return fieldError("timezone", "неизвестный часовой пояс IANA")
	}
	return nil
}
