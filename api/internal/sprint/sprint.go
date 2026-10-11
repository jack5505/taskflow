// Package sprint — спринты проекта (ТЗ §3.4, §5).
package sprint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Состояния §3.4.
const (
	StatePlanned = "planned"
	StateActive  = "active"
	StateClosed  = "closed"
)

// MaxDays — §5.2: длительность спринта не больше 42 дней.
const MaxDays = 42

var (
	ErrNotFound      = apierr.NotFound("Спринт не найден")
	ErrForbidden     = apierr.Forbidden("Спринтами управляет менеджер проекта")
	ErrAlreadyActive = apierr.Conflict(apierr.CodeSprintAlreadyActive, "В проекте уже есть активный спринт")
)

func fieldError(field, reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: field, Reason: reason}})
}

// conflict — 409 INVALID_TRANSITION с причиной в details.reason (§5.2, §5.4, §5.5).
func conflict(reason, message string) error {
	return apierr.Conflict(apierr.CodeInvalidTransition, message).With("reason", reason)
}

// Sprint — строка sprints.
type Sprint struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID uuid.UUID  `gorm:"type:uuid" json:"project_id"`
	Name      string     `json:"name"`
	Goal      *string    `json:"goal"`
	StartDate *task.Date `gorm:"type:date" json:"start_date" swaggertype:"string" example:"2026-10-13"`
	EndDate   *task.Date `gorm:"type:date" json:"end_date" swaggertype:"string" example:"2026-10-24"`
	State     string     `json:"state" enums:"planned,active,closed"`
	ClosedAt  *time.Time `json:"closed_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (Sprint) TableName() string { return "sprints" }

// View — спринт в ответе: число задач и сумма оценок (итог секции §7.4), просрочен ли (§5.3:
// активный после end_date по поясу проекта — помечается, но не закрывается).
type View struct {
	Sprint
	TasksCount      int  `json:"tasks_count"`
	EstimateMinutes int  `json:"estimate_minutes"`
	Overdue         bool `json:"overdue"`
}

// Scope — объём на старте (§5.2): задачи, сумма оценок, сколько без оценки.
type Scope struct {
	Tasks           int `json:"tasks"`
	EstimateMinutes int `json:"estimate_minutes"`
	WithoutEstimate int `json:"without_estimate"`
}

// Service — спринты. Права — через project.Service (§2: спринтами управляет менеджер).
type Service struct {
	db       *gorm.DB
	projects *project.Service
	now      func() time.Time
}

func NewService(db *gorm.DB, projects *project.Service) *Service {
	return &Service{db: db, projects: projects, now: time.Now}
}

const viewSelect = `SELECT s.*,
	(SELECT count(*) FROM tasks t WHERE t.sprint_id = s.id AND t.deleted_at IS NULL) AS tasks_count,
	(SELECT COALESCE(SUM(t.estimate_minutes), 0) FROM tasks t WHERE t.sprint_id = s.id AND t.deleted_at IS NULL) AS estimate_minutes,
	(s.state = 'active' AND s.end_date < (?::timestamptz AT TIME ZONE p.timezone)::date) AS overdue
	FROM sprints s JOIN projects p ON p.id = s.project_id`

// List — спринты проекта: активный, запланированные по дате начала, затем закрытые (новые первыми).
func (s *Service) List(ctx context.Context, a *project.Access) ([]View, error) {
	list := []View{}
	err := s.db.WithContext(ctx).Raw(viewSelect+` WHERE s.project_id = ?
		ORDER BY CASE s.state WHEN 'active' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END,
			CASE WHEN s.state = 'closed' THEN NULL ELSE s.start_date END NULLS LAST,
			s.closed_at DESC NULLS LAST, s.created_at`, s.now(), a.Project.ID).Scan(&list).Error
	if err != nil {
		return nil, fmt.Errorf("sprint: list: %w", err)
	}
	return list, nil
}

// Get — спринт с доступом к его проекту (не участник — 404).
func (s *Service) Get(ctx context.Context, u *user.User, id uuid.UUID) (*View, *project.Access, error) {
	var key string
	if err := s.db.WithContext(ctx).Raw(`SELECT p.key FROM sprints s JOIN projects p ON p.id = s.project_id WHERE s.id = ?`, id).
		Scan(&key).Error; err != nil {
		return nil, nil, fmt.Errorf("sprint: find: %w", err)
	}
	if key == "" {
		return nil, nil, ErrNotFound
	}
	a, err := s.projects.Access(ctx, u, key)
	if errors.Is(err, project.ErrNotFound) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	v, err := s.view(s.db.WithContext(ctx), id)
	if err != nil {
		return nil, nil, err
	}
	return v, a, nil
}

func (s *Service) view(tx *gorm.DB, id uuid.UUID) (*View, error) {
	var v View
	res := tx.Raw(viewSelect+` WHERE s.id = ?`, s.now(), id).Scan(&v)
	if res.Error != nil {
		return nil, fmt.Errorf("sprint: view: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return &v, nil
}

// Input — поля спринта (§3.4). Даты — ГГГГ-ММ-ДД; Clear* — явная очистка в PATCH.
type Input struct {
	Name      *string
	Goal      *string
	StartDate *string
	EndDate   *string
	ClearGoal bool
	ClearDate struct{ Start, End bool }
}

// Create — новый запланированный спринт (менеджер). Без названия — «Спринт N», где N —
// порядковый номер спринта в проекте (§3.4); номер считается под блокировкой проекта.
func (s *Service) Create(ctx context.Context, a *project.Access, in Input) (*View, error) {
	if !a.CanManage() {
		return nil, ErrForbidden
	}
	sp := Sprint{ID: uuid.New(), ProjectID: a.Project.ID, State: StatePlanned}
	if err := apply(&sp, in); err != nil {
		return nil, err
	}
	var v *View
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := lockProject(tx, a)
		if err != nil {
			return err
		}
		if in.Name == nil {
			var n int64
			if err := tx.Model(&Sprint{}).Where("project_id = ?", p.ID).Count(&n).Error; err != nil {
				return fmt.Errorf("sprint: count: %w", err)
			}
			sp.Name = fmt.Sprintf("Спринт %d", n+1)
		}
		if err := tx.Omit("ClosedAt", "CreatedAt", "UpdatedAt").Create(&sp).Error; err != nil {
			return fmt.Errorf("sprint: create: %w", err)
		}
		v, err = s.view(tx, sp.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Update — PATCH спринта (менеджер). Закрытый спринт неизменяем (§5.4) — 409.
func (s *Service) Update(ctx context.Context, u *user.User, id uuid.UUID, in Input) (*View, error) {
	var v *View
	err := s.locked(ctx, u, id, func(tx *gorm.DB, sp *Sprint, _ *project.Project) error {
		if sp.State == StateClosed {
			return conflict("closed", "Завершённый спринт не изменяется")
		}
		if err := apply(sp, in); err != nil {
			return err
		}
		if err := tx.Model(sp).Select("Name", "Goal", "StartDate", "EndDate").Updates(sp).Error; err != nil {
			return fmt.Errorf("sprint: update: %w", err)
		}
		var err error
		v, err = s.view(tx, sp.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Delete — удаление запланированного спринта (§5.5), его задачи возвращаются в бэклог.
func (s *Service) Delete(ctx context.Context, u *user.User, id uuid.UUID) error {
	return s.locked(ctx, u, id, func(tx *gorm.DB, sp *Sprint, _ *project.Project) error {
		if sp.State != StatePlanned {
			return conflict("not_planned", "Удалить можно только запланированный спринт")
		}
		if _, err := task.MoveSprintTasks(tx, sp.ProjectID, sp.ID, nil, task.SprintMove{IncludeDeleted: true}, u.ID); err != nil {
			return err
		}
		if err := tx.Delete(sp).Error; err != nil {
			return fmt.Errorf("sprint: delete: %w", err)
		}
		return nil
	})
}

// Start — запуск (§5.2). Условия под блокировкой: спринт planned, в проекте нет другого активного
// (409 SPRINT_ALREADY_ACTIVE), даты заполнены, end > start, не больше 42 дней, в спринте есть
// задачи (иначе 409 INVALID_TRANSITION с details.reason). Фиксирует объём на старте.
func (s *Service) Start(ctx context.Context, u *user.User, id uuid.UUID) (*View, *Scope, error) {
	var v *View
	var scope Scope
	err := s.locked(ctx, u, id, func(tx *gorm.DB, sp *Sprint, p *project.Project) error {
		if sp.State != StatePlanned {
			return conflict("not_planned", "Запустить можно только запланированный спринт")
		}
		var active int64
		if err := tx.Model(&Sprint{}).Where("project_id = ? AND state = 'active'", p.ID).Count(&active).Error; err != nil {
			return fmt.Errorf("sprint: active check: %w", err)
		}
		if active > 0 {
			return ErrAlreadyActive
		}
		if err := checkDates(sp, true); err != nil {
			return err
		}
		if err := tx.Raw(`SELECT count(*) AS tasks, COALESCE(SUM(estimate_minutes), 0) AS estimate_minutes,
				count(*) FILTER (WHERE estimate_minutes IS NULL) AS without_estimate
			FROM tasks WHERE sprint_id = ? AND deleted_at IS NULL`, sp.ID).Scan(&scope).Error; err != nil {
			return fmt.Errorf("sprint: scope: %w", err)
		}
		if scope.Tasks == 0 {
			return conflict("empty", "В спринте нет задач")
		}
		if err := tx.Exec(`INSERT INTO sprint_start_scope (sprint_id, task_id, estimate_minutes)
			SELECT ?, id, COALESCE(estimate_minutes, 0) FROM tasks WHERE sprint_id = ? AND deleted_at IS NULL`, sp.ID, sp.ID).Error; err != nil {
			return fmt.Errorf("sprint: scope save: %w", err)
		}
		if err := tx.Model(sp).Updates(map[string]any{"state": StateActive}).Error; err != nil {
			return fmt.Errorf("sprint: start: %w", err)
		}
		if err := events.Publish(tx, events.SprintStarted, map[string]any{
			"sprint_id": sp.ID, "project_id": p.ID, "key": p.Key, "name": sp.Name, "by": u.ID,
			"start_date": sp.StartDate, "end_date": sp.EndDate, "tasks": scope.Tasks,
			"estimate_minutes": scope.EstimateMinutes, "without_estimate": scope.WithoutEstimate,
		}); err != nil {
			return err
		}
		var err error
		v, err = s.view(tx, sp.ID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return v, &scope, nil
}

// locked — спринт FOR UPDATE, затем проект FOR UPDATE (§5: запуск, завершение и удаление — под
// блокировкой строки проекта). Порядок «спринт → проект» тот же, что у изменений задач
// («спринт → задача → проект»), иначе встречные транзакции встали бы в deadlock. Права менеджера
// и архив проверяются под блокировкой.
func (s *Service) locked(ctx context.Context, u *user.User, id uuid.UUID, fn func(*gorm.DB, *Sprint, *project.Project) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sp Sprint
		res := tx.Raw(`SELECT * FROM sprints WHERE id = ? FOR UPDATE`, id).Scan(&sp)
		if res.Error != nil {
			return fmt.Errorf("sprint: lock: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		var p project.Project
		pr := tx.Raw(`SELECT * FROM projects WHERE id = ? AND deleted_at IS NULL FOR UPDATE`, sp.ProjectID).Scan(&p)
		if pr.Error != nil {
			return fmt.Errorf("sprint: project: %w", pr.Error)
		}
		if pr.RowsAffected == 0 {
			return ErrNotFound
		}
		manager, err := canManage(tx, p.ID, u.ID)
		if err != nil {
			return err
		}
		if !manager {
			return ErrForbidden
		}
		if err := project.EnsureWritable(&p); err != nil {
			return err
		}
		return fn(tx, &sp, &p)
	})
}

// lockProject — строка проекта FOR UPDATE с перепроверкой прав и архива (создание спринта).
func lockProject(tx *gorm.DB, a *project.Access) (*project.Project, error) {
	var p project.Project
	res := tx.Raw(`SELECT * FROM projects WHERE id = ? AND deleted_at IS NULL FOR UPDATE`, a.Project.ID).Scan(&p)
	if res.Error != nil {
		return nil, fmt.Errorf("sprint: project: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, project.ErrNotFound
	}
	if err := project.EnsureWritable(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// canManage — менеджер проекта или активный администратор системы; не участник — 404.
func canManage(tx *gorm.DB, projectID, userID uuid.UUID) (bool, error) {
	var me []struct {
		IsSystemAdmin bool
		IsActive      bool
	}
	if err := tx.Raw(`SELECT is_system_admin, is_active FROM users WHERE id = ?`, userID).Scan(&me).Error; err != nil {
		return false, fmt.Errorf("sprint: actor: %w", err)
	}
	if len(me) == 0 || !me[0].IsActive {
		return false, ErrNotFound
	}
	role, err := project.RoleIn(tx, projectID, userID)
	if err != nil {
		return false, err
	}
	if role == "" && !me[0].IsSystemAdmin {
		return false, ErrNotFound
	}
	return me[0].IsSystemAdmin || role == project.RoleManager, nil
}

// apply — поля Input в спринт с проверками §3.4.
func apply(sp *Sprint, in Input) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if n := utf8.RuneCountInString(name); n < 1 || n > 100 {
			return fieldError("name", "от 1 до 100 символов")
		}
		sp.Name = name
	}
	if in.ClearGoal {
		sp.Goal = nil
	}
	if in.Goal != nil {
		g := strings.TrimSpace(*in.Goal)
		if utf8.RuneCountInString(g) > 1000 {
			return fieldError("goal", "не длиннее 1000 символов")
		}
		if g == "" {
			sp.Goal = nil
		} else {
			sp.Goal = &g
		}
	}
	for _, d := range []struct {
		field string
		value *string
		clear bool
		dst   **task.Date
	}{{"start_date", in.StartDate, in.ClearDate.Start, &sp.StartDate}, {"end_date", in.EndDate, in.ClearDate.End, &sp.EndDate}} {
		if d.clear {
			if sp.State == StateActive {
				return fieldError(d.field, "у активного спринта даты обязательны")
			}
			*d.dst = nil
		}
		if d.value != nil {
			t, err := time.Parse(time.DateOnly, *d.value)
			if err != nil {
				return fieldError(d.field, "дата ГГГГ-ММ-ДД")
			}
			*d.dst = &task.Date{Time: t}
		}
	}
	return checkDates(sp, false)
}

// checkDates — §5.2: end_date позже start_date и не дальше 42 дней. required — обе даты обязательны
// (запуск): нарушение — 409 INVALID_TRANSITION; при правке — 400 по полю end_date.
func checkDates(sp *Sprint, required bool) error {
	if sp.StartDate == nil || sp.EndDate == nil {
		if required {
			return conflict("dates", "Для запуска нужны даты начала и окончания")
		}
		return nil
	}
	days := int(sp.EndDate.Sub(sp.StartDate.Time).Hours() / 24)
	if days >= 1 && days <= MaxDays {
		return nil
	}
	if required {
		return conflict("dates", "Окончание спринта — позже начала и не дальше 42 дней")
	}
	return fieldError("end_date", "позже даты начала и не дальше 42 дней от неё")
}

// WithClock подменяет часы (тесты просрочки спринта).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}
