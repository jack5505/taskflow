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

// Brief — ключ и часовой пояс проекта.
type Brief struct {
	ID       uuid.UUID
	Key      string
	Timezone string
}

// LiveBriefs — неудалённые проекты из ids по id; openOnly — ещё и не архивные. Блокировок не берёт.
func LiveBriefs(db *gorm.DB, ids []uuid.UUID, openOnly bool) (map[uuid.UUID]Brief, error) {
	out := make(map[uuid.UUID]Brief, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []Brief
	if err := db.Raw(`SELECT id, key, timezone FROM projects WHERE id IN ? AND deleted_at IS NULL AND NOT (? AND is_archived)`,
		ids, openOnly).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: briefs: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// Memberships — какие пары «проект — пользователь» из projectIDs × userIDs — участие. Блокировок не берёт.
func Memberships(db *gorm.DB, projectIDs, userIDs []uuid.UUID) (map[Membership]bool, error) {
	out := map[Membership]bool{}
	if len(projectIDs) == 0 || len(userIDs) == 0 {
		return out, nil
	}
	var rows []Membership
	if err := db.Raw(`SELECT project_id, user_id FROM project_members WHERE project_id IN ? AND user_id IN ?`,
		projectIDs, userIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: memberships: %w", err)
	}
	for _, r := range rows {
		out[r] = true
	}
	return out, nil
}
