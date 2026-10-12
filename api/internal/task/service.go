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
	"github.com/jack5505/taskflow/api/internal/sprint/sprintdb"
	"github.com/jack5505/taskflow/api/internal/user"
)

var (
	ErrNotFound    = apierr.NotFound("Задача не найдена")
	errManagerOnly = apierr.Forbidden("Исполнителя и спринт при создании задаёт менеджер проекта")
)

// keyRe — ключ задачи {project.key}-{number}, например TM-42.
var keyRe = regexp.MustCompile(`^([A-Z]{2,10})-([1-9][0-9]{0,9})$`)

// ValidKey — key похож на ключ задачи (TM-42, в верхнем регистре): бот проверяет аргумент
// команды до вызова сервиса, чтобы ответить подсказкой о формате, а не «не найдена».
func ValidKey(key string) bool { return keyRe.MatchString(key) }

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
	now      func() time.Time
	files    attachmentStore // вложения (§3.9): WithAttachments
}

func NewService(db *gorm.DB, projects *project.Service) *Service {
	return &Service{db: db, projects: projects, now: time.Now}
}

// WithClock подменяет часы (тесты просрочки).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
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
		// Проверяются они после прав — под блокировкой проекта.
		var pre preLock
		if in.AssigneeID != nil {
			pre.users = []uuid.UUID{*in.AssigneeID}
		}
		if in.SprintID != nil {
			pre.sprints = []uuid.UUID{*in.SprintID}
		}
		if err := user.LockShare(tx, pre.users); err != nil {
			return err
		}
		if err := sprintdb.LockShare(tx, pre.sprints); err != nil {
			return err
		}
		// Номер выдаёт UPDATE строки проекта — он же блокирует проект и отвергает архивный.
		num, err := project.NextTaskNumber(tx, p.ID)
		if err != nil {
			return err
		}
		// Под блокировкой проекта состав участников не меняется: перепроверяем права и исполнителя.
		manager, err := project.ManagerIn(tx, p.ID, u.ID)
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
		if in.SprintID != nil {
			if err := checkSprint(tx, p.ID, *in.SprintID); err != nil {
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
		if err := sprintChanged(tx, t, nil, t.SprintID, u.ID); err != nil {
			return err
		}
		// Назначение при создании — такое же назначение (§7.7, §8.7): уведомление исполнителю.
		if err := publishAssigned(tx, t, u.ID); err != nil {
			return err
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
	var err error
	if t.Title, err = validTitle(in.Title); err != nil {
		return nil, err
	}
	if t.Description, err = validDescription(in.Description); err != nil {
		return nil, err
	}
	if in.Type != "" {
		if err := validType(in.Type); err != nil {
			return nil, err
		}
		t.Type = in.Type
	}
	if in.Priority != "" {
		if err := validPriority(in.Priority); err != nil {
			return nil, err
		}
		t.Priority = in.Priority
	}
	if err := validEstimate(in.EstimateMinutes); err != nil {
		return nil, err
	}
	t.EstimateMinutes = in.EstimateMinutes
	if t.DueDate, err = parseDue(in.DueDate); err != nil {
		return nil, err
	}
	if t.Labels, err = normalizeLabels(in.Labels); err != nil {
		return nil, err
	}
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

// checkAssignee — исполнитель активен и участник проекта. Строку пользователя вызывающий уже
// держит FOR SHARE (lockShare), строку проекта — тоже: деактивация и исключение из проекта ждут.
func checkAssignee(tx *gorm.DB, projectID, userID uuid.UUID) error {
	active, err := user.IsActive(tx, userID)
	if err != nil {
		return err
	}
	if !active {
		return fieldError("assignee_id", "не активный участник проекта")
	}
	role, err := project.RoleIn(tx, projectID, userID)
	if err != nil {
		return err
	}
	if role == "" {
		return fieldError("assignee_id", "не активный участник проекта")
	}
	return nil
}

// checkSprint — спринт этого проекта и не завершён (строку блокирует вызывающий — lockShare).
func checkSprint(tx *gorm.DB, projectID, sprintID uuid.UUID) error {
	state, err := sprintdb.StateIn(tx, projectID, sprintID)
	if err != nil {
		return err
	}
	if state == "" {
		return fieldError("sprint_id", "нет такого спринта в проекте")
	}
	if state == sprintdb.StateClosed {
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
	t, err := taskByKey(s.db.WithContext(ctx), a.Project.ID, key)
	if err != nil {
		return nil, nil, err
	}
	if t == nil || (t.DeletedAt != nil && !a.Admin) {
		return nil, nil, ErrNotFound
	}
	return t, a, nil
}

// History — история задачи по порядку.
func (s *Service) History(ctx context.Context, t *Task) ([]History, error) {
	return taskHistory(s.db.WithContext(ctx), t.ID)
}

// writeHistory — запись §3.7 в транзакции изменения; кто и откуда — из контекста запроса.
func writeHistory(tx *gorm.DB, taskID uuid.UUID, field string, oldValue, newValue any) error {
	return writeHistoryNote(tx, taskID, field, oldValue, newValue, "")
}

// writeHistoryNote — запись истории с пояснением (например, «снят при деактивации»).
func writeHistoryNote(tx *gorm.DB, taskID uuid.UUID, field string, oldValue, newValue any, note string) error {
	actor, source := audit.From(tx.Statement.Context)
	h := History{ID: uuid.New(), TaskID: taskID, UserID: actor, Field: field, Source: historySource(source)}
	if note != "" {
		h.Note = &note
	}
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

// commentSource — источник комментария §3.6: telegram для бота, иначе web.
func commentSource(s string) string {
	if historySource(s) == "telegram" {
		return "telegram"
	}
	return "web"
}

func jsonValue(v any) (*events.JSON, error) {
	if isNil(v) {
		return nil, nil // «пусто» — SQL NULL, а не JSON null
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("task: history value: %w", err)
	}
	j := events.JSON(b)
	return &j, nil
}

// GitHubLinks — ветки и PR задачи (§12.3): участник, удалённую — администратор (как карточка).
func (s *Service) GitHubLinks(ctx context.Context, u *user.User, key string) ([]Link, error) {
	t, _, err := s.Get(ctx, u, key)
	if err != nil {
		return nil, err
	}
	return LinksOf(s.db.WithContext(ctx), t.ID)
}
