package telegram

import "time"

// SetClock подменяет часы подключения групп (истёкший код).
func (g *Groups) SetClock(now func() time.Time) { g.now = now }

// SetClock подменяет часы карточек (секунда объединения правок).
func (c *Cards) SetClock(now func() time.Time) { c.now = now }

// SetClock подменяет часы сообщений о событиях (секунда объединения).
func (ge *GroupEvents) SetClock(now func() time.Time) { ge.now = now }

// Plural и DateRange — для табличного теста.
var (
	Plural    = plural
	DateRange = dateRange
)
