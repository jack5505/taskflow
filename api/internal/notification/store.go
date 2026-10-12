package notification

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Запросы колокольчика (notifications) и напоминаний о сроке (due_reminders).

// pageCursor — ключ продолжения: время и id последнего выданного уведомления.
type pageCursor struct {
	at time.Time
	id uuid.UUID
}

// userNotifications — уведомления пользователя не старше since (unreadOnly — только непрочитанные)
// после after, новые первыми, до limit.
func userNotifications(db *gorm.DB, userID uuid.UUID, since time.Time, unreadOnly bool, after *pageCursor, limit int) ([]Notification, error) {
	where := []string{"user_id = ?", "created_at >= ?"}
	args := []any{userID, since}
	if unreadOnly {
		where = append(where, "read_at IS NULL")
	}
	if after != nil {
		where, args = append(where, "(created_at, id) < (?, ?)"), append(args, after.at, after.id)
	}
	items := []Notification{}
	if err := db.Raw(`SELECT * FROM notifications WHERE `+strings.Join(where, " AND ")+
		` ORDER BY created_at DESC, id DESC LIMIT ?`, append(args, limit)...).Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("notification: list: %w", err)
	}
	return items, nil
}

// dueKey — записанное напоминание: задача и вид.
type dueKey struct {
	TaskID uuid.UUID
	Kind   string
}

// insertDue записывает напоминания cand одним запросом; уже записанные раньше пропускаются
// (ON CONFLICT). Возвращает записанные.
func insertDue(tx *gorm.DB, cand []Due) ([]dueKey, error) {
	values := make([]string, len(cand))
	args := make([]any, 0, 4*len(cand))
	for i, r := range cand {
		values[i] = "(?, ?, ?, ?)"
		args = append(args, r.TaskID, r.Kind, r.DueDate.Format(time.DateOnly), r.UserID)
	}
	var inserted []dueKey
	if err := tx.Raw(`INSERT INTO due_reminders (task_id, kind, due_date, user_id) VALUES `+strings.Join(values, ", ")+`
		ON CONFLICT DO NOTHING RETURNING task_id, kind`, args...).Scan(&inserted).Error; err != nil {
		return nil, fmt.Errorf("notification: due reminders: %w", err)
	}
	return inserted, nil
}

// deleteDueBefore удаляет записи о напоминаниях со сроком раньше before; возвращает их число.
func deleteDueBefore(db *gorm.DB, before time.Time) (int64, error) {
	res := db.Exec(`DELETE FROM due_reminders WHERE due_date < ?`, before.Format(time.DateOnly))
	if res.Error != nil {
		return 0, fmt.Errorf("notification: cleanup due: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// unreadSince — число непрочитанных уведомлений пользователя не старше since.
func unreadSince(db *gorm.DB, userID uuid.UUID, since time.Time) (int64, error) {
	var unread int64
	if err := db.Model(&Notification{}).Where("user_id = ? AND read_at IS NULL AND created_at >= ?", userID, since).
		Count(&unread).Error; err != nil {
		return 0, fmt.Errorf("notification: unread: %w", err)
	}
	return unread, nil
}

// parseCursor — курсор страницы; пустой — nil (первая страница).
func parseCursor(c string) (*pageCursor, error) {
	if c == "" {
		return nil, nil
	}
	at, id, err := decodeCursor(c)
	if err != nil {
		return nil, err
	}
	return &pageCursor{at, id}, nil
}
