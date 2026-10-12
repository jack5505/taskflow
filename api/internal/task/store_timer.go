package task

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Запросы таймеров (§6.4): active_timers и задачи таймеров.

// timerTaskRow — таймер с ключом и названием задачи.
type timerTaskRow struct {
	ActiveTimer
	Key   string
	Title string
}

// timerWithTask — таймер пользователя с его задачей; нет — пустой срез.
func timerWithTask(db *gorm.DB, userID uuid.UUID) ([]timerTaskRow, error) {
	var rows []timerTaskRow
	if err := db.Raw(`SELECT a.*, t.key, t.title FROM active_timers a JOIN tasks t ON t.id = a.task_id
		WHERE a.user_id = ?`, userID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: current timer: %w", err)
	}
	return rows, nil
}

// timerRows — запущенные таймеры с их задачами (projectID — только задач этого проекта), по
// пользователю. Таймер у пользователя один, таблица маленькая.
func timerRows(db *gorm.DB, projectID *uuid.UUID) ([]timerRow, error) {
	var rows []timerRow
	if err := db.Raw(`SELECT a.user_id, a.started_at, t.deleted_at IS NOT NULL AS deleted, t.status, t.assignee_id, t.project_id
		FROM active_timers a JOIN tasks t ON t.id = a.task_id
		WHERE (CAST(? AS uuid) IS NULL OR t.project_id = ?) ORDER BY a.user_id`, projectID, projectID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: timers: %w", err)
	}
	return rows, nil
}

// lockTaskTimersExcept — таймеры задачи taskID, кроме таймера пользователя keep, FOR UPDATE в порядке пользователя.
func lockTaskTimersExcept(tx *gorm.DB, taskID, keep uuid.UUID) ([]ActiveTimer, error) {
	var timers []ActiveTimer
	if err := tx.Raw(`SELECT * FROM active_timers WHERE task_id = ? AND user_id <> ? ORDER BY user_id FOR UPDATE`,
		taskID, keep).Scan(&timers).Error; err != nil {
		return nil, fmt.Errorf("task: task timers: %w", err)
	}
	return timers, nil
}

// lockTimerKey — advisory-блокировка таймера пользователя до конца транзакции.
func lockTimerKey(tx *gorm.DB, userID uuid.UUID) error {
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('timer:' || ?))`, userID.String()).Error; err != nil {
		return fmt.Errorf("task: timer lock: %w", err)
	}
	return nil
}

// timerTaskIDs — задача таймера пользователя без блокировки (пусто — таймера нет).
func timerTaskIDs(tx *gorm.DB, userID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if err := tx.Raw(`SELECT task_id FROM active_timers WHERE user_id = ?`, userID).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("task: timer: %w", err)
	}
	return ids, nil
}

// lockTaskRow — задача id (и удалённая), строка FOR UPDATE; нет — nil.
func lockTaskRow(tx *gorm.DB, id uuid.UUID) (*Task, error) {
	var t Task
	res := tx.Raw(`SELECT * FROM tasks WHERE id = ? FOR UPDATE`, id).Scan(&t)
	if res.Error != nil {
		return nil, fmt.Errorf("task: timer task: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &t, nil
}

// lockUserTimers — строка таймера пользователя FOR UPDATE.
func lockUserTimers(tx *gorm.DB, userID uuid.UUID) ([]ActiveTimer, error) {
	var timers []ActiveTimer
	if err := tx.Raw(`SELECT * FROM active_timers WHERE user_id = ? FOR UPDATE`, userID).Scan(&timers).Error; err != nil {
		return nil, fmt.Errorf("task: timer lock: %w", err)
	}
	return timers, nil
}

// deleteUserTimer удаляет таймер пользователя.
func deleteUserTimer(tx *gorm.DB, userID uuid.UUID) error {
	if err := tx.Exec(`DELETE FROM active_timers WHERE user_id = ?`, userID).Error; err != nil {
		return fmt.Errorf("task: timer delete: %w", err)
	}
	return nil
}
