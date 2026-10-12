package task

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Запросы комментариев (§3.6) и вложений (§3.9).

// taskComments — комментарии задачи по времени, в list.
func taskComments(db *gorm.DB, taskID uuid.UUID, list *[]CommentView) error {
	if err := db.Raw(`SELECT *, updated_at > created_at AS edited FROM comments
		WHERE task_id = ? ORDER BY created_at, id`, taskID).Scan(list).Error; err != nil {
		return fmt.Errorf("task: comments: %w", err)
	}
	return nil
}

// commentsByID — комментарий id (пусто — нет).
func commentsByID(tx *gorm.DB, id uuid.UUID) ([]CommentView, error) {
	var v []CommentView
	if err := tx.Raw(`SELECT *, updated_at > created_at AS edited FROM comments WHERE id = ?`, id).Scan(&v).Error; err != nil {
		return nil, fmt.Errorf("task: comment view: %w", err)
	}
	return v, nil
}

// commentTaskKey — ключ задачи комментария id; нет — "".
func commentTaskKey(tx *gorm.DB, id uuid.UUID) (string, error) {
	var key string
	if err := tx.Raw(`SELECT t.key FROM comments c JOIN tasks t ON t.id = c.task_id WHERE c.id = ?`, id).Scan(&key).Error; err != nil {
		return "", fmt.Errorf("task: comment: %w", err)
	}
	return key, nil
}

// lockComment — комментарий id задачи taskID, строка FOR UPDATE; нет — nil.
func lockComment(tx *gorm.DB, id, taskID uuid.UUID) (*Comment, error) {
	var c Comment
	res := tx.Raw(`SELECT * FROM comments WHERE id = ? AND task_id = ? FOR UPDATE`, id, taskID).Scan(&c)
	if res.Error != nil {
		return nil, fmt.Errorf("task: comment lock: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &c, nil
}

// taskAttachments — вложения задачи, новые первыми, в list.
func taskAttachments(db *gorm.DB, taskID uuid.UUID, list *[]AttachmentView) error {
	if err := db.Raw(`SELECT * FROM attachments WHERE task_id = ? ORDER BY created_at DESC, id`, taskID).
		Scan(list).Error; err != nil {
		return fmt.Errorf("task: attachments: %w", err)
	}
	return nil
}

// attachmentTaskKey — ключ задачи вложения id; нет — "".
func attachmentTaskKey(db *gorm.DB, id uuid.UUID) (string, error) {
	var key string
	if err := db.Raw(`SELECT t.key FROM attachments a JOIN tasks t ON t.id = a.task_id WHERE a.id = ?`, id).
		Scan(&key).Error; err != nil {
		return "", fmt.Errorf("task: attachment: %w", err)
	}
	return key, nil
}

// attachmentByID — вложение id; нет — nil.
func attachmentByID(db *gorm.DB, id uuid.UUID) (*Attachment, error) {
	var a Attachment
	res := db.Raw(`SELECT * FROM attachments WHERE id = ?`, id).Scan(&a)
	if res.Error != nil {
		return nil, fmt.Errorf("task: attachment: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &a, nil
}

// lockAttachment — вложение id задачи taskID, строка FOR UPDATE; нет — nil.
func lockAttachment(tx *gorm.DB, id, taskID uuid.UUID) (*Attachment, error) {
	var a Attachment
	res := tx.Raw(`SELECT * FROM attachments WHERE id = ? AND task_id = ? FOR UPDATE`, id, taskID).Scan(&a)
	if res.Error != nil {
		return nil, fmt.Errorf("task: attachment lock: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &a, nil
}
