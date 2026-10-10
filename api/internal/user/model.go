// Package user — пользователь системы (ТЗ §3.1).
package user

import (
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
)

// User — строка таблицы users. Схема создаётся миграциями; теги только описывают колонки для GORM.
type User struct {
	model.Base
	Email                string      `gorm:"type:citext;not null;uniqueIndex" json:"email"`
	PasswordHash         string      `gorm:"not null" json:"-"`
	DisplayName          string      `gorm:"size:80;not null" json:"display_name"`
	Timezone             string      `gorm:"not null;default:Europe/Moscow" json:"timezone"`
	TelegramUserID       *int64      `gorm:"uniqueIndex" json:"telegram_user_id,omitempty"`
	TelegramUsername     *string     `json:"telegram_username,omitempty"`
	IsSystemAdmin        bool        `gorm:"not null;default:false" json:"is_system_admin"`
	IsActive             bool        `gorm:"not null;default:true" json:"is_active"`
	NotificationSettings events.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"notification_settings"`
}

func (User) TableName() string { return "users" }

// DefaultTimezone — часовой пояс по умолчанию для пользователей и проектов (ТЗ §3.1, §11).
const DefaultTimezone = "Europe/Moscow"
