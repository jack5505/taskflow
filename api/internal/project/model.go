// Package project — проект и его участники (ТЗ §3.2).
package project

import (
	"time"

	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
)

// Роли участника проекта (ТЗ §2, §3.2). Администратор системы — глобальный флаг у пользователя.
const (
	RoleManager = "manager"
	RoleMember  = "member"
)

// Project — строка таблицы projects.
type Project struct {
	model.Base
	Key                  string      `gorm:"not null;uniqueIndex" json:"key"`
	Name                 string      `gorm:"size:100;not null" json:"name"`
	Description          *string     `json:"description,omitempty"`
	TaskCounter          int         `gorm:"not null;default:0" json:"-"`
	TelegramChatID       *int64      `json:"telegram_chat_id,omitempty"`
	IsArchived           bool        `gorm:"not null;default:false" json:"is_archived"`
	Timezone             string      `gorm:"not null;default:Europe/Moscow" json:"timezone"`
	NotificationSettings events.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"notification_settings"`
	DailySummaryTime     string      `gorm:"type:time;not null;default:'10:00'" json:"daily_summary_time"`
	DeletedAt            *time.Time  `json:"deleted_at,omitempty"`
}

func (Project) TableName() string { return "projects" }

// Member — участник проекта с ролью; пара project_id + user_id уникальна.
type Member struct {
	ProjectID uuid.UUID `gorm:"type:uuid;primaryKey" json:"project_id"`
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"user_id"`
	Role      string    `gorm:"not null" json:"role"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (Member) TableName() string { return "project_members" }

// ValidRole сообщает, допустима ли роль.
func ValidRole(role string) bool { return role == RoleManager || role == RoleMember }
