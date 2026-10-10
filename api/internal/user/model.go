// Package user — пользователь системы (ТЗ §3.1).
package user

import (
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
	NotificationSettings events.JSON `gorm:"type:jsonb;not null" json:"notification_settings"`
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
