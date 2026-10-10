// Package model — общие части моделей GORM.
package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Base — поля, которые есть у всех сущностей (ТЗ §3): id UUID v4, created_at, updated_at.
// Схема создаётся миграциями; структура только описывает колонки для GORM.
type Base struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// BeforeCreate выдаёт UUID v4, если он не задан.
func (b *Base) BeforeCreate(_ *gorm.DB) error {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	return nil
}
