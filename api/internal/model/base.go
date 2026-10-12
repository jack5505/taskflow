// Package model — общие части моделей GORM.
package model

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DefaultTimezone — часовой пояс по умолчанию для пользователей и проектов (ТЗ §3.1, §3.2, §11).
const DefaultTimezone = "Europe/Moscow"

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

// TimeOfDay — время суток без даты для колонок типа time, всегда в виде "15:04".
// База отдаёт "15:04:05", а GORM-умолчание задаётся как "15:04" — Scan приводит к одному виду,
// чтобы ответ после Create и после чтения из базы совпадал.
type TimeOfDay string

func (t TimeOfDay) String() string { return string(t) }

// Value отдаёт базе "15:04:00".
func (t TimeOfDay) Value() (driver.Value, error) {
	if t == "" {
		return nil, nil
	}
	parsed, err := time.Parse("15:04", string(t))
	if err != nil {
		return nil, fmt.Errorf("TimeOfDay %q: ожидается ЧЧ:ММ", t)
	}
	return parsed.Format("15:04:05"), nil
}

// Scan принимает text ("10:00:00"), []byte и time.Time — как ни вернёт драйвер.
func (t *TimeOfDay) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*t = ""
		return nil
	case time.Time:
		*t = TimeOfDay(v.Format("15:04"))
		return nil
	case []byte:
		return t.parse(string(v))
	case string:
		return t.parse(v)
	}
	return errors.New("TimeOfDay: неподдерживаемый тип из базы")
}

func (t *TimeOfDay) parse(s string) error {
	for _, layout := range []string{"15:04:05", "15:04"} {
		if parsed, err := time.Parse(layout, s); err == nil {
			*t = TimeOfDay(parsed.Format("15:04"))
			return nil
		}
	}
	return fmt.Errorf("TimeOfDay: не разобрать %q", s)
}

// locations — загруженные пояса: time.LoadLocation каждый раз читает tzdata, а воркер считает
// даты по поясам пользователей и проектов раз в минуту.
var locations sync.Map

// Location — пояс IANA tz из кэша.
func Location(tz string) (*time.Location, error) {
	if loc, ok := locations.Load(tz); ok {
		return loc.(*time.Location), nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("timezone %q: %w", tz, err)
	}
	stored, _ := locations.LoadOrStore(tz, loc)
	return stored.(*time.Location), nil
}

// ValidTimezone — пояс IANA, который можно загрузить (tzdata в образе). "Local" и пустой — нет.
func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}
