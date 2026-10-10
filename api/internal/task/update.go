package task

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Opt — поле PATCH: Set — передано ли поле, Value == nil при Set — явный null (очистить).
type Opt[T any] struct {
	Set   bool
	Value *T
}

func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// UpdateInput — PATCH /tasks/{key}: меняются только переданные поля; статус — только переходами §4.
type UpdateInput struct {
	Title           Opt[string]
	Description     Opt[string]
	Type            Opt[string]
	Priority        Opt[string]
	AssigneeID      Opt[uuid.UUID]
	SprintID        Opt[uuid.UUID]
	EstimateMinutes Opt[int]
	DueDate         Opt[string]
	Labels          Opt[[]string]
}

var (
	errEditForbidden   = apierr.Forbidden("Редактировать задачу может её автор, исполнитель или менеджер проекта")
	errAssignForbidden = apierr.Forbidden("Назначать исполнителя может менеджер проекта; взять задачу себе — «Взять в работу»")
	errSprintForbidden = apierr.Forbidden("Добавлять задачу в спринт и убирать из него может менеджер проекта")
	errEstimateManager = apierr.Forbidden("Оценку задачи в работе и позже меняет менеджер проекта")
)

// Update — PATCH задачи с оптимистической блокировкой (§3.3): ifMatch — версия, которую видел
// клиент; разошлась — 409 VERSION_CONFLICT с текущим состоянием. Права — §2, правила — §4.
func (s *Service) Update(ctx context.Context, u *user.User, key string, ifMatch int, in UpdateInput) (*Task, error) {
	var lock []uuid.UUID
	if in.AssigneeID.Value != nil {
		lock = append(lock, *in.AssigneeID.Value)
	}
	return s.change(ctx, u, key, &ifMatch, lock, func(tx *gorm.DB, t *Task, a actor) error {
		return applyUpdate(tx, t, a, in)
	})
}

func applyUpdate(tx *gorm.DB, t *Task, a actor, in UpdateInput) error {
	uid := a.user.ID
	isAuthor := t.ReporterID == uid
	isAssignee := t.AssigneeID != nil && *t.AssigneeID == uid
	if !a.manager && !isAuthor && !isAssignee {
		return errEditForbidden
	}
	var changed []string
	track := func(field string, oldV, newV any, history bool) error {
		changed = append(changed, field)
		if !history {
			return nil
		}
		return writeHistory(tx, t.ID, field, oldV, newV)
	}

	if in.Title.Set {
		if in.Title.Value == nil {
			return fieldError("title", "от 1 до 200 символов")
		}
		title := strings.TrimSpace(*in.Title.Value)
		if n := utf8.RuneCountInString(title); n < 1 || n > 200 {
			return fieldError("title", "от 1 до 200 символов")
		}
		if title != t.Title {
			if err := track("title", t.Title, title, true); err != nil {
				return err
			}
			t.Title = title
		}
	}
	if in.Description.Set {
		var d *string
		if in.Description.Value != nil && strings.TrimSpace(*in.Description.Value) != "" {
			if utf8.RuneCountInString(*in.Description.Value) > 20000 {
				return fieldError("description", "не длиннее 20 000 символов")
			}
			d = in.Description.Value
		}
		if !sameString(t.Description, d) {
			changed = append(changed, "description") // описание в историю §3.7 не входит
			t.Description = d
		}
	}
	if in.Type.Set {
		if in.Type.Value == nil || !slices.Contains([]string{TypeTask, TypeBug, TypeFeature}, *in.Type.Value) {
			return fieldError("type", "task, bug или feature")
		}
		if *in.Type.Value != t.Type {
			if err := track("type", t.Type, *in.Type.Value, true); err != nil {
				return err
			}
			t.Type = *in.Type.Value
		}
	}
	if in.Priority.Set {
		if in.Priority.Value == nil || !slices.Contains([]string{PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical}, *in.Priority.Value) {
			return fieldError("priority", "low, medium, high или critical")
		}
		if *in.Priority.Value != t.Priority {
			if err := track("priority", t.Priority, *in.Priority.Value, true); err != nil {
				return err
			}
			t.Priority = *in.Priority.Value
		}
	}
	if in.EstimateMinutes.Set && !sameInt(t.EstimateMinutes, in.EstimateMinutes.Value) {
		if v := in.EstimateMinutes.Value; v != nil && (*v < 1 || *v > 60000) {
			return fieldError("estimate_minutes", "от 1 до 60 000 минут")
		}
		if !a.manager && t.Status != StatusTodo && t.Status != StatusCancelled {
			return errEstimateManager
		}
		if err := track("estimate_minutes", t.EstimateMinutes, in.EstimateMinutes.Value, true); err != nil {
			return err
		}
		t.EstimateMinutes = in.EstimateMinutes.Value
	}
	if in.DueDate.Set {
		var d *Date
		if in.DueDate.Value != nil {
			parsed, err := time.Parse(time.DateOnly, *in.DueDate.Value)
			if err != nil {
				return fieldError("due_date", "дата ГГГГ-ММ-ДД")
			}
			d = &Date{parsed}
		}
		if !sameDate(t.DueDate, d) {
			if err := track("due_date", t.DueDate, d, true); err != nil {
				return err
			}
			t.DueDate = d
		}
	}
	if in.Labels.Set {
		var raw []string
		if in.Labels.Value != nil {
			raw = *in.Labels.Value
		}
		labels, err := normalizeLabels(raw)
		if err != nil {
			return err
		}
		if !slices.Equal([]string(labels), []string(t.Labels)) {
			changed = append(changed, "labels") // метки в историю §3.7 не входят
			t.Labels = labels
		}
	}
	if in.AssigneeID.Set && !sameID(t.AssigneeID, in.AssigneeID.Value) {
		if !a.manager {
			return errAssignForbidden
		}
		if t.Status == StatusDone || t.Status == StatusCancelled {
			return fieldError("assignee_id", "в закрытой задаче исполнителя не меняют")
		}
		if in.AssigneeID.Value == nil && t.Status == StatusInProgress {
			return fieldError("assignee_id", "с задачи в работе исполнителя не снимают: сначала верните её в «К выполнению»")
		}
		if v := in.AssigneeID.Value; v != nil {
			// Активность нового исполнителя проверена под FOR SHARE в change(); исключение из
			// проекта блокирует ту же строку пользователя, так что членство здесь не устареет.
			if err := checkAssignee(tx, t.ProjectID, *v); err != nil {
				return err
			}
		}
		changed = append(changed, "assignee_id")
		if err := setAssignee(tx, t, in.AssigneeID.Value, uid); err != nil {
			return err
		}
	}
	if in.SprintID.Set && !sameID(t.SprintID, in.SprintID.Value) {
		if !a.manager {
			return errSprintForbidden
		}
		if v := in.SprintID.Value; v != nil {
			if err := checkSprintLock(tx, t.ProjectID, *v, ""); err != nil {
				return err
			}
		}
		changed = append(changed, "sprint_id")
		if err := moveTo(tx, t, in.SprintID.Value); err != nil {
			return err
		}
	}
	if len(changed) == 0 {
		return errNoChange
	}
	return events.Publish(tx, events.TaskUpdated, map[string]any{
		"task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "by": uid, "fields": changed,
	})
}

func sameString(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func sameInt(a, b *int) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
func sameDate(a, b *Date) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(b.Time))
}
