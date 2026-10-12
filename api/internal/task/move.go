package task

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

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

// Move — перенос между секциями (менеджер) и порядок внутри секции (любой участник).
// Перестановки в проекте сериализуются (reorderProject в change()): два переноса не получат одну
// позицию, а перенумерация секции не встанет в deadlock со встречным переносом.
func (s *Service) Move(ctx context.Context, u *user.User, key string, in MoveInput) (*Task, error) {
	pre := preLock{reorder: true}
	if in.SprintID.Value != nil {
		pre.sprints = []uuid.UUID{*in.SprintID.Value}
	}
	return s.change(ctx, u, key, nil, pre, func(tx *gorm.DB, t *Task, a actor) error {
		fields := []string{"position"}
		if in.SprintID.Set && !samePtr(in.SprintID.Value, t.SprintID) {
			if err := changeSprint(tx, t, a, in.SprintID.Value); err != nil {
				return err
			}
			fields = append([]string{"sprint_id"}, fields...)
		} else if same, err := alreadyAfter(tx, t, in.AfterTaskID); err != nil {
			return err
		} else if same {
			return errNoChange // уже на этом месте — без новой версии и события
		}
		pos, err := placeAfter(tx, t, in.AfterTaskID)
		if err != nil {
			return err
		}
		t.Position = pos
		return events.Publish(tx, events.TaskUpdated, map[string]any{
			"task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "by": u.ID, "fields": fields,
		})
	})
}

// alreadyAfter — задача уже стоит сразу после after (nil — уже первая в секции).
func alreadyAfter(tx *gorm.DB, t *Task, after *uuid.UUID) (bool, error) {
	section, err := sectionRows(tx, t)
	if err != nil {
		return false, err
	}
	var prev *uuid.UUID
	for i := range section {
		if section[i].Position < t.Position || (section[i].Position == t.Position && section[i].Number < t.Number) {
			prev = &section[i].ID
		}
	}
	return samePtr(prev, after), nil
}

type neighbour struct {
	ID       uuid.UUID
	Position float64
	Number   int
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
		if err := setPosition(tx, n.ID, float64(i+1)*positionStep); err != nil {
			return 0, err
		}
	}
	return float64(idx+1)*positionStep + positionStep/2, nil
}
