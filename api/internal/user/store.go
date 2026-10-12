package user

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
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
	rows, err := TelegramRecipients(db, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int64, len(rows))
	for _, r := range rows {
		out[r.ID] = r.ChatID
	}
	return out, nil
}

// LockRow блокирует строку пользователя id FOR UPDATE (нет такого — ничего не блокирует).
func LockRow(tx *gorm.DB, id uuid.UUID) error {
	if err := tx.Exec(`SELECT 1 FROM users WHERE id = ? FOR UPDATE`, id).Error; err != nil {
		return fmt.Errorf("user: lock: %w", err)
	}
	return nil
}

// LockActive — активный пользователь id, строка FOR UPDATE; нет или деактивирован — nil.
func LockActive(tx *gorm.DB, id uuid.UUID) (*User, error) {
	var u User
	res := tx.Raw(`SELECT * FROM users WHERE id = ? AND is_active FOR UPDATE`, id).Scan(&u)
	if res.Error != nil {
		return nil, fmt.Errorf("user: lock: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &u, nil
}

// TelegramTaken — Telegram-аккаунт tgID привязан к другому пользователю, не exceptID.
func TelegramTaken(tx *gorm.DB, tgID int64, exceptID uuid.UUID) (bool, error) {
	var n int64
	if err := tx.Raw(`SELECT count(*) FROM users WHERE telegram_user_id = ? AND id <> ?`, tgID, exceptID).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("user: telegram taken: %w", err)
	}
	return n > 0, nil
}

// SetTelegram привязывает Telegram tgID с именем username к заблокированному пользователю u
// (поля u обновляются). Тот же tgID уже у другого — ошибка gorm.ErrDuplicatedKey без обёртки.
func SetTelegram(tx *gorm.DB, u *User, tgID int64, username *string) error {
	return tx.Model(u).Updates(map[string]any{"telegram_user_id": tgID, "telegram_username": username}).Error
}

// ClearTelegram снимает привязку Telegram у пользователя id; без привязки ничего не меняет.
func ClearTelegram(db *gorm.DB, id uuid.UUID) error {
	if err := db.Exec(`UPDATE users SET telegram_user_id = NULL, telegram_username = NULL, updated_at = now()
		WHERE id = ? AND telegram_user_id IS NOT NULL`, id).Error; err != nil {
		return fmt.Errorf("user: unlink telegram: %w", err)
	}
	return nil
}

// ActiveByTelegram — активный пользователь, привязанный к Telegram tgID; не привязан — nil.
func ActiveByTelegram(db *gorm.DB, tgID int64) (*User, error) {
	var u User
	res := db.Raw(`SELECT * FROM users WHERE telegram_user_id = ? AND is_active`, tgID).Scan(&u)
	if res.Error != nil {
		return nil, fmt.Errorf("user: by telegram id: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &u, nil
}

// SetTelegramUsername запоминает новое имя Telegram пользователя u (поле u обновляется);
// служебное поле — updated_at профиля не меняется.
func SetTelegramUsername(db *gorm.DB, u *User, username *string) error {
	if err := db.Model(u).UpdateColumn("telegram_username", username).Error; err != nil {
		return fmt.Errorf("user: update telegram username: %w", err)
	}
	return nil
}

// AdminTelegramIDs — Telegram активных администраторов системы с привязкой, по id пользователя.
func AdminTelegramIDs(db *gorm.DB) ([]int64, error) {
	var ids []int64
	if err := db.Raw(`SELECT telegram_user_id FROM users
		WHERE is_system_admin AND is_active AND telegram_user_id IS NOT NULL ORDER BY id`).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("user: admin telegram ids: %w", err)
	}
	return ids, nil
}

// Recipient — активный пользователь с привязанным Telegram: личный чат и настройки уведомлений §8.7.
type Recipient struct {
	ID       uuid.UUID
	ChatID   int64 // личный чат = telegram_user_id
	Settings events.JSON
}

// TelegramRecipients — активные пользователи из ids с привязанным Telegram (порядок не задан).
func TelegramRecipients(db *gorm.DB, ids []uuid.UUID) ([]Recipient, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []Recipient
	if err := db.Raw(`SELECT id, telegram_user_id AS chat_id, notification_settings AS settings FROM users
		WHERE id IN ? AND is_active AND telegram_user_id IS NOT NULL`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: telegram recipients: %w", err)
	}
	return rows, nil
}

// NamesOrdered — имена пользователей ids по display_name, затем по id (сортировка базы, как в
// ORDER BY отчётов); кого нет — того нет в ответе.
func NamesOrdered(db *gorm.DB, ids []uuid.UUID) ([]Name, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []Name
	if err := db.Raw(`SELECT id, display_name, telegram_username FROM users WHERE id IN ? ORDER BY display_name, id`, ids).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: names: %w", err)
	}
	return rows, nil
}
