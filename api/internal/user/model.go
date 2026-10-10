// Package user — пользователь системы (ТЗ §3.1).
package user

import (
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
)

// User — строка таблицы users. Схема создаётся миграциями; теги только описывают колонки для GORM.
// У IsActive нет GORM-умолчания намеренно: иначе явное false при Create подменялось бы на true.
// Новые пользователи создаются через New, где активность и настройки заданы явно.
type User struct {
	model.Base
	Email                string      `gorm:"type:citext;not null;uniqueIndex" json:"email"`
	PasswordHash         string      `gorm:"not null" json:"-"`
	DisplayName          string      `gorm:"size:80;not null" json:"display_name"`
	Timezone             string      `gorm:"not null" json:"timezone"`
	TelegramUserID       *int64      `gorm:"uniqueIndex" json:"telegram_user_id,omitempty"`
	TelegramUsername     *string     `json:"telegram_username,omitempty"`
	IsSystemAdmin        bool        `gorm:"not null" json:"is_system_admin"`
	IsActive             bool        `gorm:"not null" json:"is_active"`
	NotificationSettings events.JSON `gorm:"type:jsonb;not null" json:"notification_settings" swaggertype:"object"`
}

func (User) TableName() string { return "users" }

// New — пользователь с умолчаниями ТЗ §3.1: активен, не администратор, пояс Europe/Moscow,
// все личные уведомления §8.7 включены (пустой объект настроек).
func New(email, passwordHash, displayName string) *User {
	return &User{
		Email:                email,
		PasswordHash:         passwordHash,
		DisplayName:          displayName,
		Timezone:             model.DefaultTimezone,
		IsActive:             true,
		NotificationSettings: events.JSON("{}"),
	}
}

// MaxDisplayNameRunes — §3.1: display_name от 1 до 80 символов.
const MaxDisplayNameRunes = 80

// NormalizeDisplayName обрезает пробелы по краям и проверяет длину §3.1. Одно правило
// для API (/users) и команды admin create.
func NormalizeDisplayName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	return name, n >= 1 && n <= MaxDisplayNameRunes
}

// Brief — пользователь в списке для менеджера (§9.2: «для добавления в проект»): без привязки
// Telegram и флагов администратора.
type Brief struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
}
