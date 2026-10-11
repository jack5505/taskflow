// Package task — задачи проекта (ТЗ §3.3, §3.7, §4).
package task

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
)

// Перечисления §3.3 и статусы §4.
const (
	TypeTask    = "task"
	TypeBug     = "bug"
	TypeFeature = "feature"

	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusReview     = "review"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"

	PriorityLow      = "low"
	PriorityMedium   = "medium"
	PriorityHigh     = "high"
	PriorityCritical = "critical"
)

// Labels — метки задачи (jsonb-массив строк, §3.3).
type Labels []string

func (l Labels) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(l))
	return string(b), err
}

func (l *Labels) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		*l = Labels{}
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("task.Labels: неподдерживаемый тип из базы")
	}
	return json.Unmarshal(b, (*[]string)(l))
}

// Date — дата без времени (due_date), в JSON "2026-10-10".
type Date struct{ time.Time }

func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.Format(time.DateOnly)) }

func (d Date) Value() (driver.Value, error) { return d.Format(time.DateOnly), nil }

func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		d.Time = time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, time.UTC)
		return nil
	case string:
		t, err := time.Parse(time.DateOnly, v)
		d.Time = t
		return err
	}
	return errors.New("task.Date: неподдерживаемый тип из базы")
}

// Task — строка таблицы tasks.
type Task struct {
	model.Base
	ProjectID         uuid.UUID  `gorm:"type:uuid" json:"project_id"`
	Number            int        `json:"number"`
	Key               string     `json:"key" example:"TM-42"`
	Title             string     `json:"title"`
	Description       *string    `json:"description"`
	Type              string     `json:"type" enums:"task,bug,feature"`
	Status            string     `json:"status" enums:"todo,in_progress,review,done,cancelled"`
	Priority          string     `json:"priority" enums:"low,medium,high,critical"`
	ReporterID        uuid.UUID  `gorm:"type:uuid" json:"reporter_id"`
	AssigneeID        *uuid.UUID `gorm:"type:uuid" json:"assignee_id"`
	SprintID          *uuid.UUID `gorm:"type:uuid" json:"sprint_id"`
	EstimateMinutes   *int       `json:"estimate_minutes"`
	SpentMinutes      int        `json:"spent_minutes"`
	DueDate           *Date      `gorm:"type:date" json:"due_date" swaggertype:"string" example:"2026-10-31"`
	Labels            Labels     `gorm:"type:jsonb" json:"labels" swaggertype:"array,string"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	Position          float64    `json:"position"`
	TelegramMessageID *int64     `json:"-"`
	Version           int        `json:"version"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
}

func (Task) TableName() string { return "tasks" }

// History — строка task_history (§3.7).
type History struct {
	ID        uuid.UUID    `gorm:"type:uuid;primaryKey" json:"id"`
	TaskID    uuid.UUID    `gorm:"type:uuid" json:"-"`
	UserID    *uuid.UUID   `gorm:"type:uuid" json:"user_id"`
	Field     string       `json:"field"`
	OldValue  *events.JSON `gorm:"type:jsonb" json:"old_value"`
	NewValue  *events.JSON `gorm:"type:jsonb" json:"new_value"`
	Source    string       `json:"source" enums:"web,telegram,api"`
	CreatedAt time.Time    `json:"created_at"`
}

func (History) TableName() string { return "task_history" }

// HistoryView — запись истории в ответе API: old_value и new_value — любое JSON-значение
// (строка, число, null), поэтому тип any, а не object.
type HistoryView struct {
	ID        uuid.UUID  `json:"id"`
	UserID    *uuid.UUID `json:"user_id"`
	Field     string     `json:"field"`
	OldValue  any        `json:"old_value"`
	NewValue  any        `json:"new_value"`
	Source    string     `json:"source" enums:"web,telegram,api"`
	CreatedAt time.Time  `json:"created_at"`
}

// MarshalJSON отдаёт запись как HistoryView.
func (h History) MarshalJSON() ([]byte, error) {
	v := HistoryView{ID: h.ID, UserID: h.UserID, Field: h.Field, Source: h.Source, CreatedAt: h.CreatedAt}
	if h.OldValue != nil {
		v.OldValue = json.RawMessage(*h.OldValue)
	}
	if h.NewValue != nil {
		v.NewValue = json.RawMessage(*h.NewValue)
	}
	return json.Marshal(v)
}
