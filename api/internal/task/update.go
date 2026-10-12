package task

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/sprint/sprintdb"
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

// Update — PATCH задачи с оптимистической блокировкой (§3.3): ifMatch — версии из If-Match,
// одна из которых должна совпасть с текущей (nil — «*», без сверки); не совпала — 409
// VERSION_CONFLICT с текущим состоянием. Права — §2, правила — §4.
func (s *Service) Update(ctx context.Context, u *user.User, key string, ifMatch []int, in UpdateInput) (*Task, error) {
	var pre preLock
	if v := in.AssigneeID.Value; v != nil {
		pre.users = []uuid.UUID{*v}
	}
	if v := in.SprintID.Value; v != nil {
		pre.sprints = []uuid.UUID{*v}
	}
	return s.change(ctx, u, key, ifMatch, pre, func(tx *gorm.DB, t *Task, a actor) error {
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
	track := func(field string, oldV, newV any) error {
		changed = append(changed, field)
		return writeHistory(tx, t.ID, field, oldV, newV)
	}

	if in.Title.Set {
		if in.Title.Value == nil {
			return fieldError("title", "от 1 до 200 символов")
		}
		title, err := validTitle(*in.Title.Value)
		if err != nil {
			return err
		}
		if title != t.Title {
			if err := track("title", t.Title, title); err != nil {
				return err
			}
			t.Title = title
		}
	}
	if in.Description.Set {
		d, err := validDescription(in.Description.Value)
		if err != nil {
			return err
		}
		if !samePtr(t.Description, d) {
			changed = append(changed, "description") // описание в историю §3.7 не входит
			t.Description = d
		}
	}
	if in.Type.Set {
		if in.Type.Value == nil {
			return validType("")
		}
		if err := validType(*in.Type.Value); err != nil {
			return err
		}
		if *in.Type.Value != t.Type {
			if err := track("type", t.Type, *in.Type.Value); err != nil {
				return err
			}
			t.Type = *in.Type.Value
		}
	}
	if in.Priority.Set {
		if in.Priority.Value == nil {
			return validPriority("")
		}
		if err := validPriority(*in.Priority.Value); err != nil {
			return err
		}
		if *in.Priority.Value != t.Priority {
			if err := track("priority", t.Priority, *in.Priority.Value); err != nil {
				return err
			}
			t.Priority = *in.Priority.Value
		}
	}
	if in.EstimateMinutes.Set && !samePtr(t.EstimateMinutes, in.EstimateMinutes.Value) {
		if err := validEstimate(in.EstimateMinutes.Value); err != nil {
			return err
		}
		// Оценку задачи, которая была в работе (даже отменённой потом), меняет менеджер.
		worked := t.StartedAt != nil ||
			t.Status == StatusInProgress || t.Status == StatusReview || t.Status == StatusDone
		if worked && !a.manager {
			return errEstimateManager
		}
		if err := track("estimate_minutes", t.EstimateMinutes, in.EstimateMinutes.Value); err != nil {
			return err
		}
		t.EstimateMinutes = in.EstimateMinutes.Value
	}
	if in.DueDate.Set {
		d, err := parseDue(in.DueDate.Value)
		if err != nil {
			return err
		}
		if !sameDate(t.DueDate, d) {
			if err := track("due_date", t.DueDate, d); err != nil {
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
		if !slices.Equal(labels, t.Labels) {
			changed = append(changed, "labels") // метки в историю §3.7 не входят
			t.Labels = labels
		}
	}
	if in.AssigneeID.Set && !samePtr(t.AssigneeID, in.AssigneeID.Value) {
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
			// Строка пользователя — под FOR SHARE из change(), проекта — тоже: деактивация и
			// исключение из проекта дождутся транзакции, проверка не устареет.
			if err := checkAssignee(tx, t.ProjectID, *v); err != nil {
				return err
			}
		}
		changed = append(changed, "assignee_id")
		if err := setAssignee(tx, t, in.AssigneeID.Value, uid); err != nil {
			return err
		}
	}
	if in.SprintID.Set && !samePtr(t.SprintID, in.SprintID.Value) {
		if err := changeSprint(tx, t, a, in.SprintID.Value); err != nil {
			return err
		}
		changed = append(changed, "sprint_id")
	}
	if len(changed) == 0 {
		return errNoChange
	}
	return events.Publish(tx, events.TaskUpdated, map[string]any{
		"task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "by": uid, "fields": changed,
	})
}

// changeSprint — смена секции задачи (PATCH sprint_id и /move): только менеджер, не из
// завершённого спринта, в спринт этого проекта (не завершённый; его строка предблокирована
// в change()), с историей и журналом спринта.
func changeSprint(tx *gorm.DB, t *Task, a actor, target *uuid.UUID) error {
	if !a.manager {
		return errSprintForbidden
	}
	if err := leaveSprint(tx, t); err != nil {
		return err
	}
	if target != nil {
		if err := checkSprint(tx, t.ProjectID, *target); err != nil {
			return err
		}
	}
	return moveTo(tx, t, target, a.user.ID)
}

// leaveSprint — из завершённого спринта задачу не переносят: она остаётся в его отчёте.
// Спринт задачи читается без блокировки: закрытие блокирует спринт, затем его задачи, а строка
// задачи уже наша — закрытие ждёт нас, и состояние до конца транзакции не изменится.
func leaveSprint(tx *gorm.DB, t *Task) error {
	if t.SprintID == nil {
		return nil
	}
	state, err := sprintdb.State(tx, *t.SprintID)
	if err != nil {
		return err
	}
	if state == sprintdb.StateClosed {
		return fieldError("sprint_id", "задача из завершённого спринта — её не переносят")
	}
	return nil
}
