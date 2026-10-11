package telegram

import "time"

// SetClock подменяет часы и срок подтверждения кнопки (тест медленного обработчика).
func (p *Processor) SetClock(now func() time.Time, ackAfter time.Duration) {
	p.now, p.ackAfter = now, ackAfter
}

// SetClock подменяет часы лимитера.
func (l *Limiter) SetClock(now func() time.Time) { l.now = now }

// Limiter — лимитер отправителя (общие часы в тесте).
func (d *Deliverer) Limiter() *Limiter { return d.limit }

// ParseCommand — разбор команды для табличного теста.
func (rt *Router) ParseCommand(text string) (string, string, bool) { return rt.parseCommand(text) }

// SetClock подменяет часы привязки (истёкший код).
func (a *Accounts) SetClock(now func() time.Time) { a.now = now }
