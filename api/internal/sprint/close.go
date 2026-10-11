package sprint

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Summary — итог завершения (§5.4, сообщение бота §8.6): готово из всего, перенесено, оценки.
type Summary struct {
	Done            int      `json:"done"`
	Total           int      `json:"total"`
	Moved           int      `json:"moved"`
	MovedKeys       []string `json:"moved_keys"`
	EstimateMinutes int      `json:"estimate_minutes"`
}

// Close — завершение активного спринта (§5.4). Незакрытые задачи (todo, in_progress, review)
// переносятся в бэклог (moveTo == nil) или в запланированный спринт этого проекта; done и
// cancelled остаются. Спринт переходит в closed, заполняется closed_at; событие sprint.closed.
func (s *Service) Close(ctx context.Context, u *user.User, id uuid.UUID, moveTo *uuid.UUID) (*View, *Summary, error) {
	var v *View
	var sum Summary
	err := s.lockedWith(ctx, u, id, moveTo, func(tx *gorm.DB, sp, target *Sprint, p *project.Project) error {
		if sp.State != StateActive {
			return conflict("not_active", "Завершить можно только активный спринт")
		}
		if target != nil && target.State != StatePlanned {
			return fieldError("move_open_to", "незакрытые задачи переносятся в бэклог или в запланированный спринт")
		}
		if err := tx.Raw(`SELECT count(*) FILTER (WHERE status = 'done') AS done, count(*) AS total,
				COALESCE(SUM(estimate_minutes), 0) AS estimate_minutes
			FROM tasks WHERE sprint_id = ? AND deleted_at IS NULL`, sp.ID).Scan(&sum).Error; err != nil {
			return fmt.Errorf("sprint: summary: %w", err)
		}
		keys, err := task.MoveSprintTasks(tx, p.ID, sp.ID, moveTo, task.SprintMove{OnlyOpen: true}, u.ID)
		if err != nil {
			return err
		}
		sum.Moved, sum.MovedKeys = len(keys), keys
		if sum.MovedKeys == nil {
			sum.MovedKeys = []string{}
		}
		if err := tx.Model(sp).Updates(map[string]any{"state": StateClosed, "closed_at": s.now()}).Error; err != nil {
			return fmt.Errorf("sprint: close: %w", err)
		}
		if err := events.Publish(tx, events.SprintClosed, map[string]any{
			"sprint_id": sp.ID, "project_id": p.ID, "key": p.Key, "name": sp.Name, "by": u.ID,
			"done": sum.Done, "total": sum.Total, "moved": sum.Moved, "moved_to": moveTo,
			"estimate_minutes": sum.EstimateMinutes,
		}); err != nil {
			return err
		}
		v, err = s.view(tx, sp.ID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return v, &sum, nil
}
