package task

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Запросы к tasks и task_history: блокировки, списки, перенос и освобождение задач.

// lockTaskOrder — advisory-блокировка порядка задач проекта (перестановка, см. Move).
func lockTaskOrder(tx *gorm.DB, projectKey string) error {
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('task-order:' || ?))`, projectKey).Error; err != nil {
		return fmt.Errorf("task: order lock: %w", err)
	}
	return nil
}

// lockTaskByKey — задача по ключу в t с блокировкой lock (FOR UPDATE / FOR SHARE); withDeleted —
// и удалённая. Нет — found = false.
func lockTaskByKey(tx *gorm.DB, key string, withDeleted bool, lock string, t *Task) (found bool, err error) {
	cond := " AND deleted_at IS NULL"
	if withDeleted {
		cond = ""
	}
	res := tx.Raw(`SELECT * FROM tasks WHERE key = ?`+cond+` `+lock, key).Scan(t)
	if res.Error != nil {
		return false, fmt.Errorf("task: lock: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// releaseAssigned освобождает открытые задачи исполнителя userID: история «снят» (исполнитель и
// in_progress → todo) с note от actor, затем UPDATE. Возвращает ключи (порядок не задан).
func releaseAssigned(tx *gorm.DB, userID uuid.UUID, actor any, src, note string) ([]string, error) {
	open := `FROM tasks WHERE assignee_id = ? AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled')`
	if err := tx.Exec(`INSERT INTO task_history (task_id, user_id, field, old_value, new_value, source, note)
		SELECT id, ?, 'assignee_id', to_jsonb(assignee_id::text), NULL, ?, ? `+open+` ORDER BY id`,
		actor, src, note, userID).Error; err != nil {
		return nil, fmt.Errorf("task: release history: %w", err)
	}
	if err := tx.Exec(`INSERT INTO task_history (task_id, user_id, field, old_value, new_value, source, note)
		SELECT id, ?, 'status', '"in_progress"'::jsonb, '"todo"'::jsonb, ?, ? `+open+` AND status = 'in_progress' ORDER BY id`,
		actor, src, note, userID).Error; err != nil {
		return nil, fmt.Errorf("task: release history: %w", err)
	}
	var keys []string
	if err := tx.Raw(`UPDATE tasks SET assignee_id = NULL, version = version + 1, updated_at = now(),
			status = CASE WHEN status = 'in_progress' THEN 'todo' ELSE status END
		WHERE assignee_id = ? AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled')
		RETURNING key`, userID).Scan(&keys).Error; err != nil {
		return nil, fmt.Errorf("task: release: %w", err)
	}
	return keys, nil
}

// listTasks — задачи по условиям where (по t) в порядке секций, position, number, до limit.
func listTasks(db *gorm.DB, where []string, args []any, limit int) ([]ListItem, error) {
	var items []ListItem
	if err := db.Raw(`SELECT t.* FROM tasks t
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY `+sectionExpr+`, t.position, t.number LIMIT ?`, append(args, limit)...).Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("task: list: %w", err)
	}
	return items, nil
}

// assignedRow — открытая задача исполнителя с проектом.
type assignedRow struct {
	MyTask
	ProjectID uuid.UUID
}

// assignedOpen — до limit открытых неудалённых задач исполнителя userID в проектах projectIDs:
// по сроку (без срока — в конце), затем по порядку проектов в projectIDs, затем по номеру.
func assignedOpen(db *gorm.DB, userID uuid.UUID, projectIDs []uuid.UUID, limit int) ([]assignedRow, error) {
	rank := make([]string, len(projectIDs))
	args := []any{userID, projectIDs}
	for i, id := range projectIDs {
		rank[i] = "WHEN ? THEN " + strconv.Itoa(i)
		args = append(args, id)
	}
	var rows []assignedRow
	if err := db.Raw(`SELECT key, title, status, due_date, spent_minutes, estimate_minutes, updated_at, sprint_id, project_id
		FROM tasks WHERE assignee_id = ? AND project_id IN ? AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled')
		ORDER BY due_date NULLS LAST, CASE project_id `+strings.Join(rank, " ")+` END, number LIMIT ?`,
		append(args, limit)...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: my tasks: %w", err)
	}
	return rows, nil
}

// trashRows — удалённые задачи по условиям where (по t) с тем, кто удалил (последняя запись
// истории deleted_at со значением, не восстановление), новые первыми, до limit.
func trashRows(db *gorm.DB, where []string, args []any, limit int, rows *[]trashRow) error {
	if err := db.Raw(`SELECT t.*, d.user_id AS deleted_by_id
		FROM tasks t
		LEFT JOIN LATERAL (
			SELECT h.user_id FROM task_history h
			WHERE h.task_id = t.id AND h.field = 'deleted_at' AND h.new_value IS NOT NULL AND h.new_value <> 'null'::jsonb
			ORDER BY h.created_at DESC, h.id DESC LIMIT 1
		) d ON true
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY t.deleted_at DESC, t.id DESC LIMIT ?`, append(args, limit)...).Scan(rows).Error; err != nil {
		return fmt.Errorf("task: trash: %w", err)
	}
	return nil
}

// setPosition — новая позиция задачи id.
func setPosition(tx *gorm.DB, id uuid.UUID, pos float64) error {
	if err := tx.Exec(`UPDATE tasks SET position = ? WHERE id = ?`, pos, id).Error; err != nil {
		return fmt.Errorf("task: renumber: %w", err)
	}
	return nil
}

// liveKeyByID — ключ неудалённой задачи id (пусто — нет).
func liveKeyByID(db *gorm.DB, id uuid.UUID) ([]string, error) {
	var keys []string
	if err := db.Raw(`SELECT key FROM tasks WHERE id = ? AND deleted_at IS NULL`, id).Scan(&keys).Error; err != nil {
		return nil, fmt.Errorf("task: key by id: %w", err)
	}
	return keys, nil
}

// movedTask — перенесённая задача.
type movedTask struct {
	ID  uuid.UUID
	Key string
}

// moveTasksWhere переносит задачи по условию cond в секцию to с позиций base + n*шаг в прежнем
// порядке: запись истории sprint_id (target — новое значение для истории), version+1.
func moveTasksWhere(tx *gorm.DB, cond string, args []any, by uuid.UUID, target any, source string, to *uuid.UUID, base float64) ([]movedTask, error) {
	if err := tx.Exec(`INSERT INTO task_history (task_id, user_id, field, old_value, new_value, source)
		SELECT id, ?, 'sprint_id', to_jsonb(sprint_id::text), to_jsonb(?::text), ? FROM tasks WHERE `+cond+` ORDER BY position, number`,
		append([]any{by, target, source}, args...)...).Error; err != nil {
		return nil, fmt.Errorf("task: sprint move history: %w", err)
	}
	var moved []movedTask
	if err := tx.Raw(`UPDATE tasks t SET sprint_id = ?, position = ? + s.rn * ?, version = t.version + 1, updated_at = now()
		FROM (SELECT id, row_number() OVER (ORDER BY position, number) AS rn FROM tasks WHERE `+cond+`) s
		WHERE t.id = s.id RETURNING t.id, t.key`, append([]any{to, base, positionStep}, args...)...).Scan(&moved).Error; err != nil {
		return nil, fmt.Errorf("task: sprint move: %w", err)
	}
	return moved, nil
}
