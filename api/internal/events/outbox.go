package events

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// JSON — jsonb-колонка. []byte напрямую pgx шлёт как bytea, поэтому Value отдаёт строку.
type JSON json.RawMessage

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "{}", nil
	}
	return string(j), nil
}

func (j *JSON) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append(JSON(nil), v...)
	case string:
		*j = JSON(v)
	default:
		return errors.New("events.JSON: неподдерживаемый тип из базы")
	}
	return nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *JSON) UnmarshalJSON(b []byte) error {
	*j = append(JSON(nil), b...)
	return nil
}

// OutboxEvent — строка таблицы outbox_events (ТЗ §3.8).
type OutboxEvent struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Type      string    `gorm:"not null"`
	Payload   JSON      `gorm:"type:jsonb;not null"`
	CreatedAt time.Time `gorm:"not null"`
	SentAt    *time.Time
}

// TableName фиксирует имя таблицы: схема создаётся миграциями.
func (OutboxEvent) TableName() string { return "outbox_events" }

// Publish записывает событие в outbox внутри транзакции tx. Вызывать только из db.Transaction,
// в которой меняются данные: так событие не потеряется и не уйдёт без коммита (ТЗ §9.4).
func Publish(tx *gorm.DB, eventType string, payload any) error {
	if eventType == "" {
		return fmt.Errorf("events: пустой тип события")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("events: payload %s: %w", eventType, err)
	}
	if payload == nil {
		raw = []byte("{}")
	}
	ev := OutboxEvent{ID: uuid.New(), Type: eventType, Payload: JSON(raw), CreatedAt: time.Now().UTC()}
	if err := tx.Create(&ev).Error; err != nil {
		return fmt.Errorf("events: outbox insert %s: %w", eventType, err)
	}
	return nil
}
