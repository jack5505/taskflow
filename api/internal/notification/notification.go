// Package notification — уведомления колокольчика в веб-интерфейсе (ТЗ §3.8, §7.7).
// Создаются обработчиками событий в воркере, хранятся 30 дней.
package notification

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Типы уведомлений §7.7. Новый тип добавляется сюда вместе с обработчиком события.
const (
	TypeMemberAdded   = "member_added"
	TypeMemberRemoved = "member_removed"
	TypeTaskDeleted   = "task_deleted"
	TypeComment       = "comment"
	TypeAssigned      = "assigned"           // вас назначили исполнителем
	TypeStatus        = "status"             // смена статуса вашей задачи (в том числе принята или отменена)
	TypeRework        = "rework"             // вашу задачу вернули на доработку (с комментарием)
	TypeReview        = "review_requested"   // задача, где вы автор, отправлена на проверку
	TypeDeactivated   = "user_deactivated"   // менеджеру: участник деактивирован, его задачи освобождены (§3.1)
	TypeTimerStopped  = "timer_auto_stopped" // таймер остановлен на отметке 12 часов (§6.4)
	TypeTimerTrimmed  = "worklog_trimmed"    // запись таймера обрезана по дневному лимиту (§6.4)
)

// Retention — §3.8: уведомления хранятся 30 дней.
const Retention = 30 * 24 * time.Hour

// Notification — строка таблицы notifications.
type Notification struct {
	ID        uuid.UUID   `gorm:"type:uuid;primaryKey" json:"id"`
	UserID    uuid.UUID   `gorm:"type:uuid" json:"-"`
	TaskID    *uuid.UUID  `gorm:"type:uuid" json:"task_id"`
	Type      string      `json:"type"`
	Payload   events.JSON `gorm:"type:jsonb" json:"payload" swaggertype:"object"`
	EventID   *uuid.UUID  `gorm:"type:uuid" json:"-"`
	ReadAt    *time.Time  `json:"read_at"`
	CreatedAt time.Time   `json:"created_at"`
}

func (Notification) TableName() string { return "notifications" }

// New — уведомление пользователю. eventID — событие outbox, из которого оно сделано:
// повтор обработки того же события второго уведомления не создаёт.
type New struct {
	UserID  uuid.UUID
	TaskID  *uuid.UUID
	Type    string
	Payload any
	EventID *uuid.UUID
}

// Create сохраняет уведомление; дубль по (event_id, user_id) молча пропускается.
func Create(ctx context.Context, db *gorm.DB, n New) error {
	payload := []byte("{}")
	if n.Payload != nil {
		var err error
		if payload, err = json.Marshal(n.Payload); err != nil {
			return fmt.Errorf("notification: payload: %w", err)
		}
	}
	row := Notification{ID: uuid.New(), UserID: n.UserID, TaskID: n.TaskID, Type: n.Type, Payload: events.JSON(payload), EventID: n.EventID}
	err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Omit("ReadAt", "CreatedAt").Create(&row).Error
	if err != nil {
		return fmt.Errorf("notification: create %s: %w", n.Type, err)
	}
	return nil
}

// MaxLimit — не больше 200 на страницу (как у прочих списков §9.2).
const MaxLimit = 200

var (
	ErrBadCursor = errors.New("notification: некорректный курсор")
	ErrBadLimit  = errors.New("notification: limit от 1 до 200")
)

// Page — уведомления пользователя, новые первыми. UnreadCount — только на первой странице.
type Page struct {
	Items       []Notification `json:"items"`
	UnreadCount *int64         `json:"unread_count,omitempty"`
	NextCursor  string         `json:"next_cursor"`
}

// List — уведомления пользователя за последние 30 дней (окно — в самом запросе: уборка раз в сутки
// его только догоняет) с курсором по (created_at, id). limit 0 — 50.
func List(ctx context.Context, db *gorm.DB, userID uuid.UUID, unreadOnly bool, limit int, cursor string, now time.Time) (*Page, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > MaxLimit {
		return nil, ErrBadLimit
	}
	since := now.Add(-Retention)
	where := []string{"user_id = ?", "created_at >= ?"}
	args := []any{userID, since}
	if unreadOnly {
		where = append(where, "read_at IS NULL")
	}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		where, args = append(where, "(created_at, id) < (?, ?)"), append(args, at, id)
	}
	items := []Notification{}
	if err := db.WithContext(ctx).Raw(`SELECT * FROM notifications WHERE `+strings.Join(where, " AND ")+
		` ORDER BY created_at DESC, id DESC LIMIT ?`, append(args, limit+1)...).Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("notification: list: %w", err)
	}
	page := &Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID.String()))
	}
	if cursor == "" {
		var unread int64
		if err := db.WithContext(ctx).Model(&Notification{}).
			Where("user_id = ? AND read_at IS NULL AND created_at >= ?", userID, since).
			Count(&unread).Error; err != nil {
			return nil, fmt.Errorf("notification: unread: %w", err)
		}
		page.UnreadCount = &unread
	}
	return page, nil
}

// MarkRead отмечает прочитанными уведомления ids пользователя или все (ids пуст).
// Чужие id молча игнорируются.
func MarkRead(ctx context.Context, db *gorm.DB, userID uuid.UUID, ids []uuid.UUID, now time.Time) (int64, error) {
	q := db.WithContext(ctx).Model(&Notification{}).Where("user_id = ? AND read_at IS NULL", userID)
	if len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	res := q.Update("read_at", now)
	return res.RowsAffected, res.Error
}

// Cleanup удаляет уведомления старше 30 дней (воркер, раз в сутки).
func Cleanup(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	res := db.WithContext(ctx).Where("created_at < ?", now.Add(-Retention)).Delete(&Notification{})
	return res.RowsAffected, res.Error
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	return at, id, nil
}
