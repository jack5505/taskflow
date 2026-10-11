package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

var (
	ErrNotFound    = apierr.NotFound("Задача не найдена")
	errManagerOnly = apierr.Forbidden("Исполнителя и спринт при создании задаёт менеджер проекта")
)

// keyRe — ключ задачи {project.key}-{number}, например TM-42.
var keyRe = regexp.MustCompile(`^([A-Z]{2,10})-([1-9][0-9]{0,9})$`)

// positionStep — шаг ручного порядка (7.4): новая задача встаёт в конец секции.
const positionStep = 1024

func fieldError(field, reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: field, Reason: reason}})
}

// Service — задачи (§3.3). Права проекта — через project.Service (§2).
type Service struct {
	db       *gorm.DB
	projects *project.Service
}

func NewService(db *gorm.DB, projects *project.Service) *Service {
	return &Service{db: db, projects: projects}
}

// CreateInput — поля новой задачи. Пустые Type и Priority — умолчания §3.3.
type CreateInput struct {
	Title           string
	Description     *string
	Type            string
	Priority        string
	AssigneeID      *uuid.UUID
	SprintID        *uuid.UUID
	EstimateMinutes *int
	DueDate         *string
	Labels          []string
}

// Create — новая задача (§3.3): номер из счётчика проекта, статус todo, автор — u.
// Исполнителя и спринт при создании задаёт только менеджер или администратор.
// Пишет task.created в outbox и историю создания.
func (s *Service) Create(ctx context.Context, a *project.Access, u *user.User, in CreateInput) (*Task, error) {
	t, err := in.validate()
	if err != nil {
		return nil, err
	}
	if (in.AssigneeID != nil || in.SprintID != nil) && !a.CanManage() {
		return nil, errManagerOnly
	}
	p := a.Project
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Порядок блокировок: строки пользователя и спринта — раньше строки проекта, как у
		// деактивации (пользователь → задачи → проект) и закрытия спринта: без deadlock.
		if in.AssigneeID != nil {
			if err := lockActiveUser(tx, *in.AssigneeID); err != nil {
				return err
			}
		}
		if in.SprintID != nil {
			if err := checkSprint(tx, p.ID, *in.SprintID); err != nil {
				return err
			}
		}
		// Номер выдаёт UPDATE строки проекта — он же блокирует проект и отвергает архивный.
		num, err := project.NextTaskNumber(tx, p.ID)
		if err != nil {
			return err
		}
		// Под блокировкой проекта состав участников не меняется: перепроверяем права и исполнителя.
		manager, err := canManage(tx, p.ID, u.ID)
		if err != nil {
			return err
		}
		if (in.AssigneeID != nil || in.SprintID != nil) && !manager {
			return errManagerOnly
		}
		if in.AssigneeID != nil {
			if err := checkAssignee(tx, p.ID, *in.AssigneeID); err != nil {
				return err
			}
		}
		pos, err := endOfSection(tx, p.ID, in.SprintID, nil)
		if err != nil {
			return err
		}
		t.ProjectID, t.Number, t.Key = p.ID, num, fmt.Sprintf("%s-%d", p.Key, num)
		t.ReporterID, t.AssigneeID, t.SprintID, t.Position = u.ID, in.AssigneeID, in.SprintID, pos
		if err := tx.Create(t).Error; err != nil {
			return fmt.Errorf("task: create: %w", err)
		}
		if err := writeHistory(tx, t.ID, "status", nil, StatusTodo); err != nil {
			return err
		}
		if t.AssigneeID != nil {
			if err := writeHistory(tx, t.ID, "assignee_id", nil, t.AssigneeID); err != nil {
				return err
			}
		}
		if t.SprintID != nil {
			if err := writeHistory(tx, t.ID, "sprint_id", nil, t.SprintID); err != nil {
				return err
			}
		}
		return events.Publish(tx, events.TaskCreated, map[string]any{
			"task_id": t.ID, "key": t.Key, "project_id": p.ID, "sprint_id": t.SprintID,
			"assignee_id": t.AssigneeID, "by": u.ID,
		})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// validate проверяет поля §3.3 и возвращает заготовку задачи.
func (in CreateInput) validate() (*Task, error) {
	t := &Task{Type: TypeTask, Priority: PriorityMedium, Status: StatusTodo, Version: 1, Labels: Labels{}}
	title := strings.TrimSpace(in.Title)
	if n := utf8.RuneCountInString(title); n < 1 || n > 200 {
		return nil, fieldError("title", "от 1 до 200 символов")
	}
	t.Title = title
	if in.Description != nil && strings.TrimSpace(*in.Description) != "" {
		if utf8.RuneCountInString(*in.Description) > 20000 {
			return nil, fieldError("description", "не длиннее 20 000 символов")
		}
		t.Description = in.Description
	}
	switch in.Type {
	case "":
	case TypeTask, TypeBug, TypeFeature:
		t.Type = in.Type
	default:
		return nil, fieldError("type", "task, bug или feature")
	}
	switch in.Priority {
	case "":
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical:
		t.Priority = in.Priority
	default:
		return nil, fieldError("priority", "low, medium, high или critical")
	}
	if in.EstimateMinutes != nil {
		if *in.EstimateMinutes < 1 || *in.EstimateMinutes > 60000 {
			return nil, fieldError("estimate_minutes", "от 1 до 60 000 минут")
		}
		t.EstimateMinutes = in.EstimateMinutes
	}
	if in.DueDate != nil {
		d, err := time.Parse(time.DateOnly, *in.DueDate)
		if err != nil {
			return nil, fieldError("due_date", "дата ГГГГ-ММ-ДД")
		}
		t.DueDate = &Date{d}
	}
	labels, err := normalizeLabels(in.Labels)
	if err != nil {
		return nil, err
	}
	t.Labels = labels
	return t, nil
}

// normalizeLabels — §3.3: до 10 меток до 30 символов; пробелы по краям обрезаются, повторы убираются.
func normalizeLabels(in []string) (Labels, error) {
	out := Labels{}
	seen := map[string]bool{}
	for _, l := range in {
		l = strings.TrimSpace(l)
		if n := utf8.RuneCountInString(l); n < 1 || n > 30 {
			return nil, fieldError("labels", "каждая метка от 1 до 30 символов")
		}
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	if len(out) > 10 {
		return nil, fieldError("labels", "не больше 10 меток")
	}
	return out, nil
}

// lockActiveUser — строка пользователя FOR SHARE (деактивация, берущая FOR UPDATE, дождётся)
// и проверка, что он активен.
func lockActiveUser(tx *gorm.DB, userID uuid.UUID) error {
	var active []bool
	if err := tx.Raw(`SELECT is_active FROM users WHERE id = ? FOR SHARE`, userID).Scan(&active).Error; err != nil {
		return fmt.Errorf("task: assignee: %w", err)
	}
	if len(active) == 0 || !active[0] {
		return fieldError("assignee_id", "не активный участник проекта")
	}
	return nil
}

// checkAssignee — исполнитель — участник проекта (вызывать под блокировкой строки проекта).
func checkAssignee(tx *gorm.DB, projectID, userID uuid.UUID) error {
	role, err := project.RoleIn(tx, projectID, userID)
	if err != nil {
		return err
	}
	if role == "" {
		return fieldError("assignee_id", "не активный участник проекта")
	}
	return nil
}

// canManage — права менеджера (§2) по состоянию в транзакции: менеджер проекта или
// администратор системы. Не участник и не администратор — 404.
func canManage(tx *gorm.DB, projectID, userID uuid.UUID) (bool, error) {
	var admin []bool
	if err := tx.Raw(`SELECT is_system_admin FROM users WHERE id = ? AND is_active`, userID).Scan(&admin).Error; err != nil {
		return false, fmt.Errorf("task: actor: %w", err)
	}
	if len(admin) == 0 {
		return false, ErrNotFound
	}
	role, err := project.RoleIn(tx, projectID, userID)
	if err != nil {
		return false, err
	}
	if role == "" && !admin[0] {
		return false, project.ErrNotFound
	}
	return admin[0] || role == project.RoleManager, nil
}

// endOfSection — позиция в конце секции (спринт или бэклог). Ветки на nil нужны индексу
// tasks_section_idx: «IS NOT DISTINCT FROM» по нему не ищется.
func endOfSection(tx *gorm.DB, projectID uuid.UUID, sprintID, exclude *uuid.UUID) (float64, error) {
	q := tx.Model(&Task{}).Select("position").Where("project_id = ? AND deleted_at IS NULL", projectID)
	if sprintID == nil {
		q = q.Where("sprint_id IS NULL")
	} else {
		q = q.Where("sprint_id = ?", *sprintID)
	}
	if exclude != nil {
		q = q.Where("id <> ?", *exclude)
	}
	var last []float64
	if err := q.Order("position DESC").Limit(1).Pluck("position", &last).Error; err != nil {
		return 0, fmt.Errorf("task: position: %w", err)
	}
	if len(last) == 0 {
		return positionStep, nil
	}
	return last[0] + positionStep, nil
}

// checkSprint — спринт этого проекта и не завершён; строка FOR SHARE (закрытие спринта дождётся).
func checkSprint(tx *gorm.DB, projectID, sprintID uuid.UUID) error {
	var states []string
	if err := tx.Raw(`SELECT state FROM sprints WHERE id = ? AND project_id = ? FOR SHARE`, sprintID, projectID).
		Scan(&states).Error; err != nil {
		return fmt.Errorf("task: sprint check: %w", err)
	}
	if len(states) == 0 {
		return fieldError("sprint_id", "нет такого спринта в проекте")
	}
	if states[0] == "closed" {
		return fieldError("sprint_id", "спринт завершён")
	}
	return nil
}

// Get — задача по ключу с доступом к её проекту. Не участник — 404; удалённую видит
// только администратор (корзина §2).
func (s *Service) Get(ctx context.Context, u *user.User, key string) (*Task, *project.Access, error) {
	m := keyRe.FindStringSubmatch(key)
	if m == nil {
		return nil, nil, ErrNotFound
	}
	a, err := s.projects.Access(ctx, u, m[1])
	if errors.Is(err, project.ErrNotFound) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	var t Task
	err = s.db.WithContext(ctx).First(&t, "project_id = ? AND key = ?", a.Project.ID, key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("task: get: %w", err)
	}
	if t.DeletedAt != nil && !a.Admin {
		return nil, nil, ErrNotFound
	}
	return &t, a, nil
}

// History — история задачи по порядку.
func (s *Service) History(ctx context.Context, t *Task) ([]History, error) {
	list := []History{}
	if err := s.db.WithContext(ctx).Where("task_id = ?", t.ID).Order("created_at, id").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("task: history: %w", err)
	}
	return list, nil
}

// writeHistory — запись §3.7 в транзакции изменения; кто и откуда — из контекста запроса.
func writeHistory(tx *gorm.DB, taskID uuid.UUID, field string, oldValue, newValue any) error {
	actor, source := audit.From(tx.Statement.Context)
	h := History{ID: uuid.New(), TaskID: taskID, UserID: actor, Field: field, Source: historySource(source)}
	var err error
	if h.OldValue, err = jsonValue(oldValue); err != nil {
		return err
	}
	if h.NewValue, err = jsonValue(newValue); err != nil {
		return err
	}
	if err := tx.Omit("CreatedAt").Create(&h).Error; err != nil {
		return fmt.Errorf("task: history %s: %w", field, err)
	}
	return nil
}

// historySource — источники §3.7: web, telegram, api (прочие — воркер и команды).
func historySource(s string) string {
	switch s {
	case audit.SourceWeb:
		return "web"
	case audit.SourceBot:
		return "telegram"
	}
	return "api"
}

func jsonValue(v any) (*events.JSON, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("task: history value: %w", err)
	}
	j := events.JSON(b)
	return &j, nil
}
