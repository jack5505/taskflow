package task

import (
	"sort"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
)

// SprintMove — какие задачи спринта переносить.
type SprintMove struct {
	OnlyOpen       bool // только todo/in_progress/review (§5.4: done и cancelled остаются)
	IncludeDeleted bool // и удалённые (удаление спринта: на него не должно остаться ссылок)
}

// MoveSprintTasks переносит задачи спринта from проекта в секцию to (nil — бэклог) в её конец,
// сохраняя их порядок; каждой — запись истории sprint_id и version+1. Вызывать в транзакции под
// блокировкой спринта from. Возвращает ключи перенесённых задач.
func MoveSprintTasks(tx *gorm.DB, projectID, from uuid.UUID, to *uuid.UUID, m SprintMove, by uuid.UUID) ([]string, error) {
	cond := `project_id = ? AND sprint_id = ?`
	args := []any{projectID, from}
	if !m.IncludeDeleted {
		cond += ` AND deleted_at IS NULL`
	}
	if m.OnlyOpen {
		cond += ` AND status IN ('todo', 'in_progress', 'review')`
	}
	base, err := maxPosition(tx, projectID, to)
	if err != nil {
		return nil, err
	}
	_, source := audit.From(tx.Statement.Context)
	var target any
	if to != nil {
		target = to.String()
	}
	moved, err := moveTasksWhere(tx, cond, args, by, target, historySource(source), to, base)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(moved))
	for _, m := range moved {
		keys = append(keys, m.Key)
		if err := events.Publish(tx, events.TaskUpdated, map[string]any{
			"task_id": m.ID, "key": m.Key, "project_id": projectID, "by": by, "fields": []string{"sprint_id", "position"},
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(keys)
	return keys, nil
}
