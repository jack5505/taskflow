package github

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Запросы к github_deliveries.

// insertDelivery запоминает доставку; уже принятая — inserted = false.
func insertDelivery(tx *gorm.DB, delivery, event string) (inserted bool, err error) {
	res := tx.Exec(`INSERT INTO github_deliveries (delivery_id, event) VALUES (?, ?) ON CONFLICT DO NOTHING`, delivery, event)
	if res.Error != nil {
		return false, fmt.Errorf("github: delivery: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// deleteDeliveriesBefore удаляет доставки, принятые раньше before; возвращает их число.
func deleteDeliveriesBefore(db *gorm.DB, before time.Time) (int64, error) {
	res := db.Exec(`DELETE FROM github_deliveries WHERE received_at < ?`, before)
	if res.Error != nil {
		return 0, fmt.Errorf("github: cleanup deliveries: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// lockIssueSync — блокировка синхронизации issue задачи taskID до конца транзакции: одновременные
// события одной задачи не создают два issue (§12.9).
func lockIssueSync(tx *gorm.DB, taskID string) error {
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, "github-issue:"+taskID).Error; err != nil {
		return fmt.Errorf("github: issue lock: %w", err)
	}
	return nil
}
