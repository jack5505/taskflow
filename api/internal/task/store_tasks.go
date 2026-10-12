package task

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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

// rowLock — режим блокировки строки задачи.
type rowLock string

const (
	forUpdate rowLock = "FOR UPDATE" // изменение полей задачи
	forShare  rowLock = "FOR SHARE"  // действие, не меняющее её поля
)

// lockTaskByKey — задача по ключу в t с блокировкой lock; withDeleted — и удалённая. Нет — found = false.
func lockTaskByKey(tx *gorm.DB, key string, withDeleted bool, lock rowLock, t *Task) (found bool, err error) {
	if lock != forUpdate && lock != forShare {
		return false, fmt.Errorf("task: unknown lock %q", lock)
	}
	cond := " AND deleted_at IS NULL"
	if withDeleted {
		cond = ""
	}
	res := tx.Raw(`SELECT * FROM tasks WHERE key = ?`+cond+` `+string(lock), key).Scan(t)
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

// listCursor — ключ продолжения списка: секция, position, number последней выданной задачи.
type listCursor struct {
	section string
	pos     float64
	num     int
}

// listTasks — задачи проекта по фильтру f (значения уже проверены сервисом) после after, в
// порядке секций, position, number, до limit.
func listTasks(db *gorm.DB, projectID uuid.UUID, f ListFilter, after *listCursor, limit int) ([]ListItem, error) {
	where := []string{"t.project_id = ?"}
	args := []any{projectID}
	if !f.IncludeDeleted {
		where = append(where, "t.deleted_at IS NULL")
	}
	add := func(cond string, vals ...any) { where, args = append(where, cond), append(args, vals...) }
	if len(f.Status) > 0 {
		add("t.status IN ?", f.Status)
	}
	if len(f.Type) > 0 {
		add("t.type IN ?", f.Type)
	}
	if len(f.Priority) > 0 {
		add("t.priority IN ?", f.Priority)
	}
	if c, v := idCondition("t.assignee_id", f.Assignee); c != "" {
		add(c, v...)
	}
	if c, v := idCondition("t.sprint_id", f.Sprint); c != "" {
		add(c, v...)
	}
	if f.Label != "" {
		add("t.labels @> jsonb_build_array(?::text)", f.Label)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		like := escapeLike(q)
		add(`(t.key ILIKE ? OR t.title ILIKE ?)`, like+"%", "%"+like+"%")
	}
	if after != nil {
		add("("+sectionExpr+", t.position, t.number) > (?, ?, ?)", after.section, after.pos, after.num)
	}
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
	if len(projectIDs) == 0 {
		return nil, nil
	}
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

// trashCursor — ключ продолжения корзины: время удаления и id последней выданной задачи.
type trashCursor struct {
	at time.Time
	id uuid.UUID
}

// trashRows — удалённые задачи проекта после after с тем, кто удалил (последняя запись истории
// deleted_at со значением, не восстановление), новые первыми, до limit.
func trashRows(db *gorm.DB, projectID uuid.UUID, after *trashCursor, limit int, rows *[]trashRow) error {
	where := []string{"t.project_id = ?", "t.deleted_at IS NOT NULL"}
	args := []any{projectID}
	if after != nil {
		where, args = append(where, "(t.deleted_at, t.id) < (?, ?)"), append(args, after.at, after.id)
	}
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

// liveKeyByID — ключ неудалённой задачи id; нет — "".
func liveKeyByID(db *gorm.DB, id uuid.UUID) (string, error) {
	var key string
	if err := db.Raw(`SELECT key FROM tasks WHERE id = ? AND deleted_at IS NULL`, id).Scan(&key).Error; err != nil {
		return "", fmt.Errorf("task: key by id: %w", err)
	}
	return key, nil
}

// movedTask — перенесённая задача.
type movedTask struct {
	ID  uuid.UUID
	Key string
}

// moveSprintRows переносит задачи спринта from проекта (m: и удалённые, только незакрытые) в
// секцию to с позиций base + n*шаг в прежнем порядке: запись истории sprint_id (target — новое
// значение для истории), version+1.
func moveSprintRows(tx *gorm.DB, projectID, from uuid.UUID, m SprintMove, by uuid.UUID, target any, source string, to *uuid.UUID, base float64) ([]movedTask, error) {
	cond := `project_id = ? AND sprint_id = ?`
	args := []any{projectID, from}
	if !m.IncludeDeleted {
		cond += ` AND deleted_at IS NULL`
	}
	if m.OnlyOpen {
		cond += ` AND status IN ('todo', 'in_progress', 'review')`
	}
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

// endOfSection — позиция в конце секции (спринт или бэклог). Ветки на nil нужны индексу
// tasks_section_idx: «IS NOT DISTINCT FROM» по нему не ищется.
func endOfSection(tx *gorm.DB, projectID uuid.UUID, sprintID, exclude *uuid.UUID) (float64, error) {
	q := inSection(tx.Model(&Task{}).Select("position").Where("project_id = ? AND deleted_at IS NULL", projectID), sprintID)
	if exclude != nil {
		q = q.Where("id <> ?", *exclude)
	}
	var last []float64
	if err := q.Order("position DESC").Limit(1).Pluck("position", &last).Error; err != nil {
		return 0, fmt.Errorf("task: position: %w", err)
	}
	if len(last) == 0 {
		return positionStep, nil
	}
	return last[0] + positionStep, nil
}

// sectionRows — задачи секции t (без самой t) по порядку.
func sectionRows(tx *gorm.DB, t *Task) ([]neighbour, error) {
	q := inSection(tx.Model(&Task{}).Select("id, position, number").Where("project_id = ? AND deleted_at IS NULL AND id <> ?", t.ProjectID, t.ID), t.SprintID)
	var rows []neighbour
	if err := q.Order("position, number").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: section: %w", err)
	}
	return rows, nil
}

// maxPosition — наибольшая позиция неудалённой задачи в секции to (nil — бэклог) проекта; пусто — 0.
func maxPosition(tx *gorm.DB, projectID uuid.UUID, to *uuid.UUID) (float64, error) {
	var base float64
	end := inSection(tx.Model(&Task{}).Select("COALESCE(MAX(position), 0)").Where("project_id = ? AND deleted_at IS NULL", projectID), to)
	if err := end.Scan(&base).Error; err != nil {
		return 0, fmt.Errorf("task: section end: %w", err)
	}
	return base, nil
}

// taskByKey — задача проекта по ключу (и удалённая); нет — nil.
func taskByKey(db *gorm.DB, projectID uuid.UUID, key string) (*Task, error) {
	var t Task
	err := db.First(&t, "project_id = ? AND key = ?", projectID, key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("task: get: %w", err)
	}
	return &t, nil
}

// taskHistory — история задачи по порядку.
func taskHistory(db *gorm.DB, taskID uuid.UUID) ([]History, error) {
	list := []History{}
	if err := db.Where("task_id = ?", taskID).Order("created_at, id").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("task: history: %w", err)
	}
	return list, nil
}

// sectionExpr — секция задачи для порядка списка: спринт (по id), бэклог — последним.
const sectionExpr = `COALESCE(t.sprint_id::text, '~')`

func idCondition(column string, f IDFilter) (string, []any) {
	switch {
	case f.Null && len(f.IDs) > 0:
		return "(" + column + " IS NULL OR " + column + " IN ?)", []any{f.IDs}
	case f.Null:
		return column + " IS NULL", nil
	case len(f.IDs) > 0:
		return column + " IN ?", []any{f.IDs}
	}
	return "", nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// inSection — условие секции: спринт sprintID или бэклог (nil). Ветки на nil нужны индексу
// tasks_section_idx: «IS NOT DISTINCT FROM» по нему не ищется.
func inSection(q *gorm.DB, sprintID *uuid.UUID) *gorm.DB {
	if sprintID == nil {
		return q.Where("sprint_id IS NULL")
	}
	return q.Where("sprint_id = ?", *sprintID)
}
