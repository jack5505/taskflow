package telegram

import "time"

// SetClock подменяет часы подключения групп (истёкший код).
func (g *Groups) SetClock(now func() time.Time) { g.now = now }

// SetClock подменяет часы карточек (секунда объединения правок).
func (c *Cards) SetClock(now func() time.Time) { c.now = now }
