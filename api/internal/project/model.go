// Package project — проект и его участники (ТЗ §3.2).
package project

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
)

// Роли участника проекта (ТЗ §2, §3.2). Администратор системы — глобальный флаг у пользователя.
const (
	RoleManager = "manager"
	RoleMember  = "member"
)

// DefaultDailySummaryTime — время ежедневной сводки по умолчанию (ТЗ §3.2, §8.6).
const DefaultDailySummaryTime model.TimeOfDay = "10:00"

// Project — строка таблицы projects. DeletedAt — gorm.DeletedAt: мягко удалённые проекты (§3.2)
// не попадают в обычные запросы сами собой; корзина и восстановление идут через Unscoped().
type Project struct {
	model.Base
	Key            string  `gorm:"not null;uniqueIndex" json:"key"`
	Name           string  `gorm:"size:100;not null" json:"name"`
	Description    *string `json:"description,omitempty"`
	TaskCounter    int     `gorm:"not null" json:"-"`
	TelegramChatID *int64  `json:"telegram_chat_id,omitempty"`
	// TelegramChatTitle — название подключённой группы на момент подключения или переименования (§7.9).
	TelegramChatTitle *string `json:"telegram_chat_title,omitempty"`
	// GitHubRepo — привязанный репозиторий owner/name (§12.1); установка App — служебное поле.
	GitHubRepo           *string         `gorm:"column:github_repo" json:"github_repo,omitempty"`
	GitHubInstallationID *int64          `gorm:"column:github_installation_id" json:"-"`
	IsArchived           bool            `gorm:"not null" json:"is_archived"`
	Timezone             string          `gorm:"not null" json:"timezone"`
	NotificationSettings events.JSON     `gorm:"type:jsonb;not null" json:"notification_settings" swaggertype:"object"`
	DailySummaryTime     model.TimeOfDay `gorm:"type:time;not null" json:"daily_summary_time"`
	DeletedAt            gorm.DeletedAt  `gorm:"index" json:"deleted_at,omitempty" swaggertype:"string" format:"date-time"`
}

func (Project) TableName() string { return "projects" }

// New — проект с умолчаниями ТЗ §3.2: счётчик задач 0, не архивный, пояс Europe/Moscow,
// сводка в 10:00, события бота §8.6 по умолчанию (пустой объект настроек).
func New(key, name string) *Project {
	return &Project{
		Key:                  key,
		Name:                 name,
		Timezone:             model.DefaultTimezone,
		NotificationSettings: events.JSON("{}"),
		DailySummaryTime:     DefaultDailySummaryTime,
	}
}

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
