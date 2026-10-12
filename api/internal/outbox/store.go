package outbox

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Запросы к outbox_events.

// lockUnsent — до limit неотправленных событий по порядку, FOR UPDATE SKIP LOCKED: несколько
// воркеров не возьмут одно событие.
func lockUnsent(tx *gorm.DB, limit int) ([]events.OutboxEvent, error) {
	var batch []events.OutboxEvent
	if err := tx.Raw(`SELECT id, type, payload, created_at, sent_at FROM outbox_events
		WHERE sent_at IS NULL ORDER BY created_at, id LIMIT ? FOR UPDATE SKIP LOCKED`, limit).
		Scan(&batch).Error; err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	return batch, nil
}

// markSent отмечает события ids отправленными (время — базы).
func markSent(tx *gorm.DB, ids []uuid.UUID) error {
	if err := tx.Exec(`UPDATE outbox_events SET sent_at = now() WHERE id IN ?`, ids).Error; err != nil {
		return fmt.Errorf("mark sent: %w", err)
	}
	return nil
}

// deleteSentBefore удаляет события, отправленные раньше, чем retention (интервал PostgreSQL) назад.
func deleteSentBefore(db *gorm.DB, retention string) (int64, error) {
	res := db.Exec(`DELETE FROM outbox_events WHERE sent_at IS NOT NULL AND sent_at < now() - ?::interval`, retention)
	return res.RowsAffected, res.Error
}
