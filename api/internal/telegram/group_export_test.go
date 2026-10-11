package telegram

import "time"

// SetClock подменяет часы подключения групп (истёкший код).
func (g *Groups) SetClock(now func() time.Time) { g.now = now }
