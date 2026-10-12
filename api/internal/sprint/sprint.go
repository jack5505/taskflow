// Package sprint — спринты проекта (ТЗ §3.4, §5).
package sprint

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
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

// startScope — строка sprint_start_scope: задача в объёме спринта на старте (§5.2), оценка без оценки — 0.
type startScope struct {
	SprintID        uuid.UUID `gorm:"type:uuid"`
	TaskID          uuid.UUID `gorm:"type:uuid"`
	EstimateMinutes int
}

func (startScope) TableName() string { return "sprint_start_scope" }

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

// views — представления спринтов rows: число неудалённых задач, сумма оценок и просрочка (§5.3:
// активный после end_date по поясу проекта).
func (s *Service) views(db *gorm.DB, rows []Sprint) ([]View, error) {
	ids := make([]uuid.UUID, len(rows))
	projectIDs := make([]uuid.UUID, 0, 1)
	for i, sp := range rows {
		ids[i] = sp.ID
		if !slices.Contains(projectIDs, sp.ProjectID) {
			projectIDs = append(projectIDs, sp.ProjectID)
		}
	}
	totals, err := task.SprintTotalsOf(db, ids)
	if err != nil {
		return nil, err
	}
	zones, err := project.Timezones(db, projectIDs, true)
	if err != nil {
		return nil, err
	}
	now := s.now()
	list := make([]View, len(rows))
	for i, sp := range rows {
		t := totals[sp.ID]
		list[i] = View{Sprint: sp, TasksCount: t.Tasks, EstimateMinutes: t.EstimateMinutes}
		if sp.State == StateActive && sp.EndDate != nil {
			today, err := dateIn(now, zones[sp.ProjectID])
			if err != nil {
				return nil, err
			}
			list[i].Overdue = sp.EndDate.Before(today)
		}
	}
	return list, nil
}

// dateIn — дата момента t по поясу tz (полночь UTC этой даты, как даты из базы).
func dateIn(t time.Time, tz string) (time.Time, error) {
	loc, err := model.Location(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("sprint: %w", err)
	}
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
}

// List — спринты проекта: активный, запланированные по дате начала, затем закрытые (новые первыми).
func (s *Service) List(ctx context.Context, a *project.Access) ([]View, error) {
	db := s.db.WithContext(ctx)
	var rows []Sprint
	if err := db.Raw(`SELECT * FROM sprints WHERE project_id = ?
		ORDER BY CASE state WHEN 'active' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END,
			CASE WHEN state = 'closed' THEN NULL ELSE start_date END NULLS LAST,
			closed_at DESC NULLS LAST, created_at`, a.Project.ID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sprint: list: %w", err)
	}
	return s.views(db, rows)
}

// Get — спринт с доступом к его проекту (не участник — 404).
func (s *Service) Get(ctx context.Context, u *user.User, id uuid.UUID) (*View, *project.Access, error) {
	var projectIDs []uuid.UUID
	if err := s.db.WithContext(ctx).Raw(`SELECT project_id FROM sprints WHERE id = ?`, id).Scan(&projectIDs).Error; err != nil {
		return nil, nil, fmt.Errorf("sprint: find: %w", err)
	}
	if len(projectIDs) == 0 {
		return nil, nil, ErrNotFound
	}
	key, err := project.KeyWithDeleted(s.db.WithContext(ctx), projectIDs[0])
	if err != nil {
		return nil, nil, err
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
	var rows []Sprint
	if err := tx.Raw(`SELECT * FROM sprints WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sprint: view: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	list, err := s.views(tx, rows)
	if err != nil {
		return nil, err
	}
	return &list[0], nil
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
		p, err := project.LockForUpdate(tx, a.Project.ID)
		if err != nil {
			return err
		}
		if err := project.EnsureWritable(p); err != nil {
			return err
		}
		manager, err := project.ManagerIn(tx, p.ID, a.UserID())
		if err != nil {
			return err
		}
		if !manager {
			return ErrForbidden
		}
		if in.Name == nil {
			if sp.Name, err = defaultName(tx, p.ID); err != nil {
				return err
			}
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
		// Объём на старте: задачи спринта уже заблокированы (locked), между чтением и записью не меняются.
		tasks, err := task.SprintScope(tx, sp.ID)
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			return conflict("empty", "В спринте нет задач")
		}
		rows := make([]startScope, len(tasks))
		for i, t := range tasks {
			rows[i] = startScope{SprintID: sp.ID, TaskID: t.ID}
			if t.EstimateMinutes == nil {
				scope.WithoutEstimate++
			} else {
				rows[i].EstimateMinutes = *t.EstimateMinutes
			}
			scope.Tasks, scope.EstimateMinutes = scope.Tasks+1, scope.EstimateMinutes+rows[i].EstimateMinutes
		}
		if err := tx.CreateInBatches(&rows, 1000).Error; err != nil { // 3 параметра на строку, лимит PostgreSQL — 65535
			return fmt.Errorf("sprint: scope: %w", err)
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
		v, err = s.view(tx, sp.ID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return v, &scope, nil
}

// locked — спринт FOR UPDATE, его задачи FOR UPDATE, затем проект FOR UPDATE (§5: запуск,
// завершение и удаление — под блокировкой строки проекта). Порядок «спринт → задачи → проект» тот
// же, что у изменений задач («спринт → задача → проект»): встречные транзакции не встают в deadlock.
// Права менеджера и архив проверяются под блокировкой.
func (s *Service) locked(ctx context.Context, u *user.User, id uuid.UUID, fn func(*gorm.DB, *Sprint, *project.Project) error) error {
	return s.lockedWith(ctx, u, id, nil, func(tx *gorm.DB, sp, _ *Sprint, p *project.Project) error {
		return fn(tx, sp, p)
	})
}

// lockedWith — locked с ещё одним спринтом (цель переноса при завершении): строки обоих спринтов
// и их задач FOR UPDATE в порядке id, затем проект. Цель не найдена или из другого проекта —
// other == nil; ошибку выбирает fn.
func (s *Service) lockedWith(ctx context.Context, u *user.User, id uuid.UUID, otherID *uuid.UUID,
	fn func(*gorm.DB, *Sprint, *Sprint, *project.Project) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ids := []uuid.UUID{id}
		if otherID != nil && *otherID != id {
			ids = append(ids, *otherID)
		}
		var rows []Sprint
		if err := tx.Raw(`SELECT * FROM sprints WHERE id IN ? ORDER BY id FOR UPDATE`, ids).Scan(&rows).Error; err != nil {
			return fmt.Errorf("sprint: lock: %w", err)
		}
		var sp, other *Sprint
		for i := range rows {
			if rows[i].ID == id {
				sp = &rows[i]
			} else {
				other = &rows[i]
			}
		}
		if sp == nil {
			return ErrNotFound
		}
		// Задачи спринтов — раньше проекта: тот же порядок, что у правок задач (задача → проект).
		// Если поднять блокировку проекта выше, вернётся deadlock с параллельными правками задач.
		if err := task.LockSprintTasks(tx, ids); err != nil {
			return err
		}
		p, err := project.LockForUpdate(tx, sp.ProjectID)
		if errors.Is(err, project.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		manager, err := project.ManagerIn(tx, p.ID, u.ID)
		if errors.Is(err, project.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !manager {
			return ErrForbidden
		}
		if err := project.EnsureWritable(p); err != nil {
			return err
		}
		if other != nil && other.ProjectID != sp.ProjectID {
			other = nil // чужой спринт — как несуществующий; решает fn, в нужном месте порядка ответов
		}
		return fn(tx, sp, other, p)
	})
}

// defaultName — «Спринт N» (§3.4): N — порядковый номер в проекте; после удаления спринта имя не
// повторяет существующее.
func defaultName(tx *gorm.DB, projectID uuid.UUID) (string, error) {
	var names []string
	if err := tx.Model(&Sprint{}).Where("project_id = ?", projectID).Pluck("name", &names).Error; err != nil {
		return "", fmt.Errorf("sprint: names: %w", err)
	}
	taken := make(map[string]bool, len(names))
	for _, n := range names {
		taken[n] = true
	}
	for n := len(names) + 1; ; n++ {
		if name := fmt.Sprintf("Спринт %d", n); !taken[name] {
			return name, nil
		}
	}
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
