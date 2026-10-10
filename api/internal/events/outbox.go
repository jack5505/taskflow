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

// OutboxEvent — строка таблицы outbox_events (ТЗ §3.8). created_at ставит база (DEFAULT now()):
// часы приложения и базы могут расходиться, а релей сортирует по этой колонке.
type OutboxEvent struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey"`
	Type      string     `gorm:"not null"`
	Payload   JSON       `gorm:"type:jsonb;not null"`
	CreatedAt time.Time  `gorm:"not null;default:now()"`
	SentAt    *time.Time `gorm:"default:null"`
}

// TableName фиксирует имя таблицы: схема создаётся миграциями.
func (OutboxEvent) TableName() string { return "outbox_events" }

var errUnknownType = errors.New("events: неизвестный тип события; добавьте его в ТЗ §9.4 и в types.go")

// Publish записывает событие в outbox внутри транзакции tx. Вызывать только из db.Transaction,
// в которой меняются данные: так событие не потеряется и не уйдёт без коммита (ТЗ §9.4).
func Publish(tx *gorm.DB, eventType string, payload any) error {
	if !Known(eventType) {
		return fmt.Errorf("%w: %q", errUnknownType, eventType)
	}
	raw := []byte("{}")
	if payload != nil {
		var err error
		if raw, err = json.Marshal(payload); err != nil {
			return fmt.Errorf("events: payload %s: %w", eventType, err)
		}
	}
	ev := OutboxEvent{ID: uuid.New(), Type: eventType, Payload: JSON(raw)}
	if err := tx.Select("ID", "Type", "Payload").Create(&ev).Error; err != nil {
		return fmt.Errorf("events: outbox insert %s: %w", eventType, err)
	}
	return nil
}
