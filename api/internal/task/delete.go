package task

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/user"
)

var errDeleteForbidden = apierr.Forbidden("Удалять задачи может менеджер проекта")

// Delete — мягкое удаление (§4, §2: менеджер): deleted_at, задача пропадает с доски, из
// бэклога, поиска и бота; записи времени остаются. История, журнал аудита (§3.8) и
// task.deleted — в той же транзакции. Запущенный на задаче таймер останавливает change() (§4).
func (s *Service) Delete(ctx context.Context, u *user.User, key string) error {
	_, err := s.change(ctx, u, key, nil, preLock{}, func(tx *gorm.DB, t *Task, a actor) error {
		if !a.manager {
			return errDeleteForbidden
		}
		now := s.now()
		if err := writeHistory(tx, t.ID, "deleted_at", nil, now); err != nil {
			return err
		}
		t.DeletedAt = &now
		if err := sprintChanged(tx, t, t.SprintID, nil, a.user.ID); err != nil {
			return err // удалённая задача уходит из объёма активного спринта
		}
		return deletedOrRestored(tx, t, events.TaskDeleted, audit.TaskDeleted, a)
	})
	return err
}

// Restore — восстановление удалённой задачи (§4, §2: только администратор системы). Удалённую
// задачу остальные не видят — для них 404. Не удалённая возвращается как есть.
func (s *Service) Restore(ctx context.Context, u *user.User, key string) (*Task, error) {
	return s.change(ctx, u, key, nil, preLock{withDeleted: true}, func(tx *gorm.DB, t *Task, a actor) error {
		if !a.admin {
			return apierr.Forbidden("Восстанавливать задачи может администратор системы")
		}
		if t.DeletedAt == nil {
			return errNoChange
		}
		if err := writeHistory(tx, t.ID, "deleted_at", t.DeletedAt, nil); err != nil {
			return err
		}
		t.DeletedAt = nil
		if err := releaseStaleAssignee(tx, t); err != nil {
			return err
		}
		if err := sprintChanged(tx, t, nil, t.SprintID, a.user.ID); err != nil {
			return err
		}
		return deletedOrRestored(tx, t, events.TaskRestored, audit.TaskRestored, a)
	})
}

// releaseStaleAssignee — пока задача лежала в корзине, исполнителя могли деактивировать или
// исключить из проекта (деактивация освобождает только не удалённые задачи). Такой исполнитель
// снимается, задача в работе возвращается в todo — как при деактивации (§3.1).
func releaseStaleAssignee(tx *gorm.DB, t *Task) error {
	if t.AssigneeID == nil {
		return nil
	}
	var ok bool
	if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM users u JOIN project_members m ON m.user_id = u.id
		WHERE u.id = ? AND u.is_active AND m.project_id = ?)`, *t.AssigneeID, t.ProjectID).Scan(&ok).Error; err != nil {
		return fmt.Errorf("task: assignee check: %w", err)
	}
	if ok {
		return nil
	}
	const note = "снят при восстановлении: исполнитель деактивирован или исключён из проекта"
	if err := writeHistoryNote(tx, t.ID, "assignee_id", t.AssigneeID, nil, note); err != nil {
		return err
	}
	t.AssigneeID = nil
	if t.Status == StatusInProgress {
		if err := writeHistoryNote(tx, t.ID, "status", t.Status, StatusTodo, note); err != nil {
			return err
		}
		t.Status = StatusTodo
	}
	return nil
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
