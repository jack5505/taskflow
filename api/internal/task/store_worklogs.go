package task

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Запросы записей времени (§6): worklogs и spent_minutes задачи.

// taskWorklogs — записи задачи, новые сверху, в list.
func taskWorklogs(db *gorm.DB, taskID uuid.UUID, list *[]WorklogView) error {
	if err := db.Raw(`SELECT * FROM worklogs WHERE task_id = ? ORDER BY work_date DESC, created_at DESC, id`, taskID).
		Scan(list).Error; err != nil {
		return fmt.Errorf("task: worklogs: %w", err)
	}
	return nil
}

// worklogTaskKey — ключ задачи записи id; нет записи — "".
func worklogTaskKey(tx *gorm.DB, id uuid.UUID) (string, error) {
	var key string
	if err := tx.Raw(`SELECT t.key FROM worklogs w JOIN tasks t ON t.id = w.task_id WHERE w.id = ?`, id).Scan(&key).Error; err != nil {
		return "", fmt.Errorf("task: worklog: %w", err)
	}
	return key, nil
}

// lockWorklog — запись id задачи taskID, строка FOR UPDATE; нет — nil.
func lockWorklog(tx *gorm.DB, id, taskID uuid.UUID) (*Worklog, error) {
	var w Worklog
	res := tx.Raw(`SELECT * FROM worklogs WHERE id = ? AND task_id = ? FOR UPDATE`, id, taskID).Scan(&w)
	if res.Error != nil {
		return nil, fmt.Errorf("task: worklog lock: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &w, nil
}

// sprintOfKey — спринт задачи по ключу без блокировки (пусто — задача вне спринта или её нет).
func sprintOfKey(tx *gorm.DB, key string) ([]uuid.UUID, error) {
	var sprints []uuid.UUID
	if err := tx.Raw(`SELECT sprint_id FROM tasks WHERE key = ? AND sprint_id IS NOT NULL`, key).Scan(&sprints).Error; err != nil {
		return nil, fmt.Errorf("task: worklog sprint: %w", err)
	}
	return sprints, nil
}

// worklogsByID — запись id (пусто — нет).
func worklogsByID(tx *gorm.DB, id uuid.UUID) ([]WorklogView, error) {
	var v []WorklogView
	if err := tx.Raw(`SELECT * FROM worklogs WHERE id = ?`, id).Scan(&v).Error; err != nil {
		return nil, fmt.Errorf("task: worklog view: %w", err)
	}
	return v, nil
}

// lockWorklogDay — advisory-блокировка записей времени пользователя до конца транзакции
// (повторный захват в той же транзакции не ждёт).
func lockWorklogDay(tx *gorm.DB, userID uuid.UUID) error {
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('worklog-day:' || ?))`, userID.String()).Error; err != nil {
		return fmt.Errorf("task: worklog lock: %w", err)
	}
	return nil
}

// dayUsed — минуты пользователя за дату по всем задачам без записи exclude. Вызывать под
// lockWorklogDay.
func dayUsed(tx *gorm.DB, userID uuid.UUID, date time.Time, exclude uuid.UUID) (int, error) {
	var used int
	if err := tx.Raw(`SELECT COALESCE(sum(minutes), 0) FROM worklogs WHERE user_id = ? AND work_date = ? AND id <> ?`,
		userID, date.Format(time.DateOnly), exclude).Scan(&used).Error; err != nil {
		return 0, fmt.Errorf("task: worklog day sum: %w", err)
	}
	return used, nil
}

// refreshSpent — spent_minutes = сумма записей задачи (§3.3). Версия задачи не растёт: запись
// времени не правит редактируемые поля, и чужое списание не должно ломать правку карточки
// через If-Match (как комментарии); spent_minutes в PATCH не принимается — потерять его нельзя.
func refreshSpent(tx *gorm.DB, t *Task) (int, error) {
	if err := tx.Raw(`UPDATE tasks SET spent_minutes = (SELECT COALESCE(sum(minutes), 0) FROM worklogs WHERE task_id = ?)
		WHERE id = ? RETURNING spent_minutes`, t.ID, t.ID).Scan(&t.SpentMinutes).Error; err != nil {
		return 0, fmt.Errorf("task: spent: %w", err)
	}
	return t.SpentMinutes, nil
}
