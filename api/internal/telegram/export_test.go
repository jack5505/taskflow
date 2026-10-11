package telegram

import (
	"context"
	"time"
)

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

// SetCommandsPause подменяет паузу между попытками setMyCommands; возвращает откат.
func SetCommandsPause(d time.Duration) func() {
	old := setCommandsPause
	setCommandsPause = func(int) time.Duration { return d }
	return func() { setCommandsPause = old }
}

// SetClock подменяет часы оповещений (период 10 минут и сутки).
func (a *Alerter) SetClock(now func() time.Time) { a.now = now }

// SetClock подменяет часы монитора (сбой дольше минуты).
func (m *Monitor) SetClock(now func() time.Time) { m.now = now }

// SetCheck подменяет проверку kind (недоступный Redis в тесте).
func (m *Monitor) SetCheck(kind string, check func(context.Context) error) { m.checks[kind] = check }

// SetDisk подменяет замер заполнения диска.
func (m *Monitor) SetDisk(disk func() (int, error)) { m.disk = disk }
