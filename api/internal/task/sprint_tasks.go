package task

import (
	"fmt"
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
	var base float64
	end := tx.Model(&Task{}).Select("COALESCE(MAX(position), 0)").Where("project_id = ? AND deleted_at IS NULL", projectID)
	if to == nil {
		end = end.Where("sprint_id IS NULL")
	} else {
		end = end.Where("sprint_id = ?", *to)
	}
	if err := end.Scan(&base).Error; err != nil {
		return nil, fmt.Errorf("task: section end: %w", err)
	}
	_, source := audit.From(tx.Statement.Context)
	var target any
	if to != nil {
		target = to.String()
	}
	if err := tx.Exec(`INSERT INTO task_history (task_id, user_id, field, old_value, new_value, source)
		SELECT id, ?, 'sprint_id', to_jsonb(sprint_id::text), to_jsonb(?::text), ? FROM tasks WHERE `+cond+` ORDER BY position, number`,
		append([]any{by, target, historySource(source)}, args...)...).Error; err != nil {
		return nil, fmt.Errorf("task: sprint move history: %w", err)
	}
	var moved []struct {
		ID  uuid.UUID
		Key string
	}
	if err := tx.Raw(`UPDATE tasks t SET sprint_id = ?, position = ? + s.rn * ?, version = t.version + 1, updated_at = now()
		FROM (SELECT id, row_number() OVER (ORDER BY position, number) AS rn FROM tasks WHERE `+cond+`) s
		WHERE t.id = s.id RETURNING t.id, t.key`, append([]any{to, base, positionStep}, args...)...).Scan(&moved).Error; err != nil {
		return nil, fmt.Errorf("task: sprint move: %w", err)
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
