package user

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Name — имя пользователя для текстов бота и писем.
type Name struct {
	ID               uuid.UUID
	DisplayName      string
	TelegramUsername *string
}

// Names — имена пользователей ids (и деактивированных) по id; кого нет — того нет в ответе.
func Names(db *gorm.DB, ids ...uuid.UUID) (map[uuid.UUID]Name, error) {
	out := make(map[uuid.UUID]Name, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []Name
	if err := db.Raw(`SELECT id, display_name, telegram_username FROM users WHERE id IN ?`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: names: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// DisplayName — имя пользователя id (и деактивированного); нет такого — ok = false.
func DisplayName(db *gorm.DB, id uuid.UUID) (name string, ok bool, err error) {
	var names []string
	if err := db.Raw(`SELECT display_name FROM users WHERE id = ?`, id).Scan(&names).Error; err != nil {
		return "", false, fmt.Errorf("user: display name: %w", err)
	}
	if len(names) == 0 {
		return "", false, nil
	}
	return names[0], true, nil
}

// ActiveTelegramIDs — Telegram-аккаунты активных пользователей из ids, у кого он привязан, по id.
func ActiveTelegramIDs(db *gorm.DB, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	out := make(map[uuid.UUID]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID             uuid.UUID
		TelegramUserID int64
	}
	if err := db.Raw(`SELECT id, telegram_user_id FROM users WHERE id IN ? AND is_active AND telegram_user_id IS NOT NULL`,
		ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: telegram ids: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r.TelegramUserID
	}
	return out, nil
}
