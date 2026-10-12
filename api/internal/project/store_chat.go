package project

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Функции для Telegram-групп проектов (§8.2): привязка группы и настройки событий §8.6.
// Права и правила подключения проверяет модуль telegram; здесь — только строки projects.

// Chat — группа проекта и настройки её событий (§8.6).
type Chat struct {
	// ChatID — группа проекта; у удалённого проекта nil: в его группу не пишут.
	ChatID   *int64
	Settings events.JSON
}

// ChatOf — группа проекта projectID, в том числе удалённого (ChatID тогда nil). Проекта нет — nil.
func ChatOf(db *gorm.DB, projectID uuid.UUID) (*Chat, error) {
	var c Chat
	res := db.Raw(`SELECT CASE WHEN deleted_at IS NULL THEN telegram_chat_id END AS chat_id, notification_settings AS settings
		FROM projects WHERE id = ?`, projectID).Scan(&c)
	if res.Error != nil {
		return nil, fmt.Errorf("project: chat: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &c, nil
}

// SettingsWithDeleted — настройки событий §8.6 проекта по ключу, в том числе удалённого (выбор
// ответа бота, не проверка прав); проекта нет — ok = false.
func SettingsWithDeleted(db *gorm.DB, key string) (settings events.JSON, ok bool, err error) {
	var raw []events.JSON
	if err := db.Raw(`SELECT notification_settings FROM projects WHERE key = ?`, key).Scan(&raw).Error; err != nil {
		return nil, false, fmt.Errorf("project: settings: %w", err)
	}
	if len(raw) != 1 {
		return nil, false, nil
	}
	return raw[0], true, nil
}

// ChatProjects — неудалённые проекты, подключённые к группе chatID, по ключу.
func ChatProjects(db *gorm.DB, chatID int64) ([]Project, error) {
	var ps []Project
	if err := db.Where("telegram_chat_id = ?", chatID).Order("key").Find(&ps).Error; err != nil {
		return nil, fmt.Errorf("project: chat projects: %w", err)
	}
	return ps, nil
}

// LockChatProjects — неудалённые проекты группы chatID по ключу, строки FOR UPDATE в порядке ключа.
func LockChatProjects(tx *gorm.DB, chatID int64) ([]Project, error) {
	var ps []Project
	if err := tx.Raw(`SELECT * FROM projects WHERE telegram_chat_id = ? AND deleted_at IS NULL ORDER BY key FOR UPDATE`,
		chatID).Scan(&ps).Error; err != nil {
		return nil, fmt.Errorf("project: chat projects: %w", err)
	}
	return ps, nil
}

// LockChatProjectIDs — id всех проектов группы chatID (и удалённых), строки FOR UPDATE.
func LockChatProjectIDs(tx *gorm.DB, chatID int64) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if err := tx.Raw(`SELECT id FROM projects WHERE telegram_chat_id = ? FOR UPDATE`, chatID).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("project: chat projects: %w", err)
	}
	return ids, nil
}

// SetChat подключает группу chatID с названием title к проекту id; строку проекта вызывающий
// уже заблокировал.
func SetChat(tx *gorm.DB, id uuid.UUID, chatID int64, title *string) error {
	if err := tx.Exec(`UPDATE projects SET telegram_chat_id = ?, telegram_chat_title = ?, updated_at = now() WHERE id = ?`,
		chatID, title, id).Error; err != nil {
		return fmt.Errorf("project: bind group: %w", err)
	}
	return nil
}

// ClearChat снимает группу с проекта id; строку проекта вызывающий уже заблокировал.
func ClearChat(tx *gorm.DB, id uuid.UUID) error {
	if err := tx.Exec(`UPDATE projects SET telegram_chat_id = NULL, telegram_chat_title = NULL, updated_at = now() WHERE id = ?`,
		id).Error; err != nil {
		return fmt.Errorf("project: unbind group: %w", err)
	}
	return nil
}

// MoveChat переписывает группу from на to у всех её проектов (группа стала супергруппой) и
// возвращает число проектов. Строки проектов вызывающий блокирует заранее (LockChatProjectIDs).
func MoveChat(tx *gorm.DB, from, to int64) (int64, error) {
	res := tx.Exec(`UPDATE projects SET telegram_chat_id = ?, updated_at = now() WHERE telegram_chat_id = ?`, to, from)
	if res.Error != nil {
		return 0, fmt.Errorf("project: migrate chat: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// RenameChat запоминает новое название группы chatID у её проектов. updated_at не меняется:
// название группы — не правка проекта пользователем.
func RenameChat(db *gorm.DB, chatID int64, title *string) error {
	if err := db.Exec(`UPDATE projects SET telegram_chat_title = ? WHERE telegram_chat_id = ?`, title, chatID).Error; err != nil {
		return fmt.Errorf("project: rename chat: %w", err)
	}
	return nil
}

// Membership — участие пользователя в проекте.
type Membership struct {
	ProjectID uuid.UUID
	UserID    uuid.UUID
}

// Managers — менеджеры проектов projectIDs (без проверки активности: её делает модуль user).
func Managers(db *gorm.DB, projectIDs []uuid.UUID) ([]Membership, error) {
	var rows []Membership
	if err := db.Raw(`SELECT project_id, user_id FROM project_members WHERE project_id IN ? AND role = ?`,
		projectIDs, RoleManager).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: managers: %w", err)
	}
	return rows, nil
}
