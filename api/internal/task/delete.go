package task

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/user"
)

var errDeleteForbidden = apierr.Forbidden("Удалять задачи может менеджер проекта")

// Delete — мягкое удаление (§4, §2: менеджер): deleted_at, задача пропадает с доски, из
// бэклога, поиска и бота; записи времени остаются. История, журнал аудита (§3.8) и
// task.deleted — в той же транзакции. Остановку запущенного таймера добавит задача таймера.
func (s *Service) Delete(ctx context.Context, u *user.User, key string) error {
	_, err := s.change(ctx, u, key, nil, preLock{}, func(tx *gorm.DB, t *Task, a actor) error {
		if !a.manager {
			return errDeleteForbidden
		}
		now := time.Now()
		if err := writeHistory(tx, t.ID, "deleted_at", nil, now); err != nil {
			return err
		}
		t.DeletedAt = &now
		return deletedOrRestored(tx, t, events.TaskDeleted, audit.TaskDeleted, a)
	})
	return err
}

// Restore — восстановление удалённой задачи (§4, §2: только администратор системы). Удалённую
// задачу остальные не видят — для них 404. Не удалённая возвращается как есть.
func (s *Service) Restore(ctx context.Context, u *user.User, key string) (*Task, error) {
	return s.change(ctx, u, key, nil, preLock{withDeleted: true}, func(tx *gorm.DB, t *Task, a actor) error {
		if !a.admin {
			if t.DeletedAt != nil {
				return ErrNotFound
			}
			return apierr.Forbidden("Восстанавливать задачи может администратор системы")
		}
		if t.DeletedAt == nil {
			return errNoChange
		}
		if err := writeHistory(tx, t.ID, "deleted_at", t.DeletedAt, nil); err != nil {
			return err
		}
		t.DeletedAt = nil
		return deletedOrRestored(tx, t, events.TaskRestored, audit.TaskRestored, a)
	})
}

func deletedOrRestored(tx *gorm.DB, t *Task, eventType, action string, a actor) error {
	if err := events.Publish(tx, eventType, map[string]any{
		"task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "by": a.user.ID,
		"assignee_id": t.AssigneeID, "reporter_id": t.ReporterID, "title": t.Title,
	}); err != nil {
		return err
	}
	return audit.Write(tx, audit.Record{ProjectID: &t.ProjectID, Action: action, EntityType: "task", EntityID: t.ID.String(),
		New: map[string]any{"key": t.Key, "title": t.Title}})
}
