package task

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Функции для карточек и сообщений бота в группе проекта (§8.3, §8.6): строки tasks без проверки
// прав — сообщения в группу собирает воркер по событиям, права проверила операция, их создавшая.

// Snapshot — задача, в том числе удалённая, со всем, что печатается в карточке и сообщениях группы.
type Snapshot struct {
	ID                uuid.UUID
	ProjectID         uuid.UUID
	Key               string
	Title             string
	Type              string
	Priority          string
	Status            string
	EstimateMinutes   *int
	SpentMinutes      int
	DueDate           *time.Time
	TelegramMessageID *int
	Deleted           bool
	ReporterID        uuid.UUID
	AssigneeID        *uuid.UUID
	SprintID          *uuid.UUID
}

// SnapshotWithDeleted — задача id, в том числе удалённая (бот показывает «Задача удалена», §4).
// Нет — nil.
func SnapshotWithDeleted(db *gorm.DB, id uuid.UUID) (*Snapshot, error) {
	var s Snapshot
	res := db.Raw(`SELECT id, project_id, key, title, type, priority, status, estimate_minutes, spent_minutes, due_date,
			telegram_message_id, deleted_at IS NOT NULL AS deleted, reporter_id, assignee_id, sprint_id
		FROM tasks WHERE id = ?`, id).Scan(&s)
	if res.Error != nil {
		return nil, fmt.Errorf("task: snapshot: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &s, nil
}

// StatusWithDeleted — статус задачи по ключу, в том числе удалённой (выбор ответа бота, не
// проверка прав); нет такой — "".
func StatusWithDeleted(db *gorm.DB, key string) (string, error) {
	var st []string
	if err := db.Raw(`SELECT status FROM tasks WHERE key = ?`, key).Scan(&st).Error; err != nil {
		return "", fmt.Errorf("task: status: %w", err)
	}
	if len(st) == 0 {
		return "", nil
	}
	return st[0], nil
}

// Brief — задача в списке бота.
type Brief struct {
	ID    uuid.UUID
	Key   string
	Title string
}

// FreeInSprint — до limit свободных задач спринта (todo без исполнителя, не удалённые) по
// приоритету, затем по порядку на доске.
func FreeInSprint(db *gorm.DB, sprintID uuid.UUID, limit int) ([]Brief, error) {
	var free []Brief
	if err := db.Raw(`SELECT id, key, title FROM tasks
		WHERE sprint_id = ? AND status = 'todo' AND assignee_id IS NULL AND deleted_at IS NULL
		ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, position, number
		LIMIT ?`, sprintID, limit).Scan(&free).Error; err != nil {
		return nil, fmt.Errorf("task: sprint free tasks: %w", err)
	}
	return free, nil
}

// IDsWithMessage — id задач с ключами keys, у которых есть карточка в группе, в том числе
// удалённых: карточку удалённой задачи тоже правят.
func IDsWithMessage(db *gorm.DB, keys []string) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if err := db.Raw(`SELECT id FROM tasks WHERE key IN ? AND telegram_message_id IS NOT NULL`, keys).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("task: tasks with card: %w", err)
	}
	return ids, nil
}

// SetMessage запоминает сообщение-карточку msgID задачи id (§8.3).
func SetMessage(db *gorm.DB, id uuid.UUID, msgID int) error {
	if err := db.Exec(`UPDATE tasks SET telegram_message_id = ? WHERE id = ?`, msgID, id).Error; err != nil {
		return fmt.Errorf("task: card message id: %w", err)
	}
	return nil
}

// ForgetMessages очищает telegram_message_id задач проектов projectIDs: карточки остались в
// прежней группе. Строки задач блокируются UPDATE после строк проектов, которые вызывающий
// уже заблокировал.
func ForgetMessages(tx *gorm.DB, projectIDs ...uuid.UUID) error {
	if len(projectIDs) == 0 {
		return nil
	}
	if err := tx.Exec(`UPDATE tasks SET telegram_message_id = NULL
		WHERE project_id IN ? AND telegram_message_id IS NOT NULL`, projectIDs).Error; err != nil {
		return fmt.Errorf("task: forget task messages: %w", err)
	}
	return nil
}
