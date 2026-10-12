package task

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Функции для модуля notification: задачи для напоминаний о сроке (§8.7) и уведомлений (§7.7).

// DueTask — открытая задача с исполнителем и сроком.
type DueTask struct {
	ID         uuid.UUID
	AssigneeID uuid.UUID
	Key        string
	Title      string
	ProjectID  uuid.UUID
	DueDate    time.Time
}

// OpenDueBetween — неудалённые незакрытые задачи с исполнителем и сроком в from..to включительно.
func OpenDueBetween(db *gorm.DB, from, to time.Time) ([]DueTask, error) {
	var rows []DueTask
	if err := db.Raw(`SELECT id, assignee_id, key, title, project_id, due_date FROM tasks
		WHERE due_date IS NOT NULL AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled') AND assignee_id IS NOT NULL
			AND due_date BETWEEN ? AND ?`, from.Format(time.DateOnly), to.Format(time.DateOnly)).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: open due: %w", err)
	}
	return rows, nil
}

// KeyRef — задача по ключу: проект и номер.
type KeyRef struct {
	ProjectID uuid.UUID
	Key       string
	Number    int
}

// RefsByKeys — задачи с ключами keys, в том числе удалённые.
func RefsByKeys(db *gorm.DB, keys []string) ([]KeyRef, error) {
	var rows []KeyRef
	if err := db.Raw(`SELECT project_id, key, number FROM tasks WHERE key IN ?`, keys).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: refs by keys: %w", err)
	}
	return rows, nil
}
