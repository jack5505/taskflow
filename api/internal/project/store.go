package project

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// KeyWithDeleted — ключ проекта id, в том числе удалённого; нет такого — "".
func KeyWithDeleted(db *gorm.DB, id uuid.UUID) (string, error) {
	var key string
	if err := db.Raw(`SELECT key FROM projects WHERE id = ?`, id).Scan(&key).Error; err != nil {
		return "", fmt.Errorf("project: key: %w", err)
	}
	return key, nil
}

// Timezones — часовые пояса проектов ids по id; withDeleted = false — без удалённых проектов.
func Timezones(db *gorm.DB, ids []uuid.UUID, withDeleted bool) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID       uuid.UUID
		Timezone string
	}
	if err := db.Raw(`SELECT id, timezone FROM projects WHERE id IN ? AND (? OR deleted_at IS NULL)`, ids, withDeleted).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: timezones: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r.Timezone
	}
	return out, nil
}
