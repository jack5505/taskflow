package task

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/user"
)

// minGap — меньший зазор между соседями уже не делится надёжно: секция перенумеровывается.
const minGap = 1e-6

// MoveInput — перетаскивание (§7.4, §9.2): SprintID не передан — та же секция; передан null —
// бэклог. AfterTaskID nil — в начало секции, иначе сразу после указанной задачи этой секции.
type MoveInput struct {
	SprintID    Opt[uuid.UUID]
	AfterTaskID *uuid.UUID
}

var errMoveSprintForbidden = apierr.Forbidden("Переносить задачи между бэклогом и спринтами может менеджер проекта")

// Move — перенос между секциями (менеджер) и порядок внутри секции (любой участник).
func (s *Service) Move(ctx context.Context, u *user.User, key string, in MoveInput) (*Task, error) {
	var pre preLock
	if in.SprintID.Value != nil {
		pre.sprints = []uuid.UUID{*in.SprintID.Value}
	}
	return s.change(ctx, u, key, nil, pre, func(tx *gorm.DB, t *Task, a actor) error {
		target := t.SprintID
		if in.SprintID.Set {
			target = in.SprintID.Value
		}
		if !samePtr(target, t.SprintID) {
			if !a.manager {
				return errMoveSprintForbidden
			}
			if err := leaveSprintAllowed(tx, t); err != nil {
				return err
			}
			if target != nil {
				if err := checkSprint(tx, t.ProjectID, *target); err != nil {
					return err
				}
			}
			if err := moveTo(tx, t, target); err != nil {
				return err
			}
		}
		pos, err := placeAfter(tx, t, in.AfterTaskID)
		if err != nil {
			return err
		}
		t.Position = pos
		return events.Publish(tx, events.TaskUpdated, map[string]any{
			"task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "by": u.ID, "fields": []string{"position"},
		})
	})
}

// leaveSprintAllowed — §5.4: из завершённого спринта задачи не переносятся.
func leaveSprintAllowed(tx *gorm.DB, t *Task) error {
	if t.SprintID == nil {
		return nil
	}
	var state string
	if err := tx.Raw(`SELECT state FROM sprints WHERE id = ?`, *t.SprintID).Scan(&state).Error; err != nil {
		return fmt.Errorf("task: sprint: %w", err)
	}
	if state == "closed" {
		return fieldError("sprint_id", "задача в завершённом спринте — переносить её нельзя")
	}
	return nil
}

type neighbour struct {
	ID       uuid.UUID
	Position float64
}

// placeAfter — позиция в секции задачи: в начало (after == nil) или сразу после after.
// Середина между соседями; если зазор исчерпан — секция перенумеровывается шагом positionStep.
func placeAfter(tx *gorm.DB, t *Task, after *uuid.UUID) (float64, error) {
	section, err := sectionRows(tx, t)
	if err != nil {
		return 0, err
	}
	idx := -1 // вставить после элемента idx (−1 — в начало)
	if after != nil {
		if *after == t.ID {
			return 0, fieldError("after_task_id", "нельзя поставить задачу после самой себя")
		}
		for i, n := range section {
			if n.ID == *after {
				idx = i
			}
		}
		if idx < 0 {
			return 0, fieldError("after_task_id", "задачи нет в этой секции")
		}
	}
	lo, hi := 0.0, 0.0
	hasLo, hasHi := idx >= 0, idx+1 < len(section)
	if hasLo {
		lo = section[idx].Position
	}
	if hasHi {
		hi = section[idx+1].Position
	}
	switch {
	case !hasLo && !hasHi:
		return positionStep, nil
	case !hasLo:
		return hi - positionStep, nil
	case !hasHi:
		return lo + positionStep, nil
	case hi-lo > minGap:
		return (lo + hi) / 2, nil
	}
	// Зазор исчерпан: перенумеровать секцию (без переносимой задачи) и вставить на место.
	for i, n := range section {
		if err := tx.Exec(`UPDATE tasks SET position = ? WHERE id = ?`, float64(i+1)*positionStep, n.ID).Error; err != nil {
			return 0, fmt.Errorf("task: renumber: %w", err)
		}
	}
	return float64(idx+1)*positionStep + positionStep/2, nil
}

// sectionRows — задачи секции t (без самой t) по порядку.
func sectionRows(tx *gorm.DB, t *Task) ([]neighbour, error) {
	q := tx.Model(&Task{}).Select("id, position").Where("project_id = ? AND deleted_at IS NULL AND id <> ?", t.ProjectID, t.ID)
	if t.SprintID == nil {
		q = q.Where("sprint_id IS NULL")
	} else {
		q = q.Where("sprint_id = ?", *t.SprintID)
	}
	var rows []neighbour
	if err := q.Order("position, number").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: section: %w", err)
	}
	return rows, nil
}
