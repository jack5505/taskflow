package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Типы сбоев §10.3: по каждому — не чаще одного оповещения за alertPeriod, диск — раз в сутки.
const (
	AlertWorker   = "worker"
	AlertPostgres = "postgres"
	AlertRedis    = "redis"
	AlertSend     = "send"
	AlertDisk     = "disk"
)

const (
	alertPeriod     = 10 * time.Minute
	diskAlertPeriod = 24 * time.Hour
	// downThreshold — §10.3: сбоем считается недоступность дольше минуты.
	downThreshold = time.Minute
	// DiskLimit — §10.3: диск сервера заполнен больше чем на 80%.
	DiskLimit = 80
	// HeartbeatKey — воркер жив: метка времени последнего сигнала, ключ живёт HeartbeatTTL.
	HeartbeatKey      = "worker:heartbeat"
	HeartbeatInterval = 15 * time.Second
	HeartbeatTTL      = 5 * time.Minute
)

// Alerter — оповещения администраторам системы в личный чат бота (§10.3). Отправка идёт напрямую
// в Bot API, мимо очереди: при сбое Redis очередь сама недоступна. Получатели — активные
// администраторы с привязанным Telegram; список кэшируется, чтобы оповестить о падении
// PostgreSQL, когда базу уже не прочитать. Частота — в памяти процесса: оповещения о базе,
// Redis, воркере и диске шлёт API, о неотправленных сообщениях — воркер.
type Alerter struct {
	db  *gorm.DB
	api API
	now func() time.Time

	mu     sync.Mutex
	last   map[string]time.Time
	admins []int64
}

func NewAlerter(db *gorm.DB, api API) *Alerter {
	return &Alerter{db: db, api: api, now: time.Now, last: map[string]time.Time{}}
}

// Notify шлёт оповещение типа kind, если такое не уходило последние 10 минут (диск — сутки).
// Отсчёт периода начинается, только если оповещение дошло хотя бы до одного администратора:
// при сбое Telegram или пустом списке получателей следующий проход попробует снова. Без бота
// (api == nil) оповещение только пишется в лог ошибок. Возвращает, ушло ли оповещение.
func (a *Alerter) Notify(ctx context.Context, kind, text string) bool {
	a.mu.Lock()
	period := alertPeriod
	if kind == AlertDisk {
		period = diskAlertPeriod
	}
	now := a.now()
	prev, seen := a.last[kind]
	if seen && now.Sub(prev) < period {
		a.mu.Unlock()
		return false
	}
	a.last[kind] = now // занимаем тип, пока идёт отправка: параллельный вызов не продублирует
	a.mu.Unlock()

	slog.Error("alert", "kind", kind, "text", text)
	delivered := a.api == nil
	if a.api != nil {
		for _, chat := range a.recipients(ctx) {
			sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
			_, err := a.api.SendMessage(sendCtx, &bot.SendMessageParams{ChatID: chat, Text: text})
			cancel()
			if err != nil {
				slog.Error("alert: send", "kind", kind, "chat_id", chat, "err", err)
				continue
			}
			delivered = true
		}
	}
	if !delivered {
		a.mu.Lock()
		if seen {
			a.last[kind] = prev
		} else {
			delete(a.last, kind)
		}
		a.mu.Unlock()
	}
	return delivered
}

// Refresh перечитывает список получателей; Monitor зовёт его на каждом проходе, пока база доступна,
// чтобы при её падении список уже был.
func (a *Alerter) Refresh(ctx context.Context) error {
	var ids []int64
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := a.db.WithContext(qctx).Raw(`SELECT telegram_user_id FROM users
		WHERE is_system_admin AND is_active AND telegram_user_id IS NOT NULL ORDER BY id`).Scan(&ids).Error; err != nil {
		return err
	}
	a.mu.Lock()
	a.admins = ids
	a.mu.Unlock()
	return nil
}

// recipients — Telegram активных администраторов; база недоступна — последний известный список.
func (a *Alerter) recipients(ctx context.Context) []int64 {
	if err := a.Refresh(ctx); err != nil {
		slog.Warn("alert: admins from cache", "err", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.admins
}

// Monitor — проверки §10.3 в процессе API: PostgreSQL, Redis, сигнал воркера, диск. Сбой длиннее
// минуты — оповещение (не чаще раза в 10 минут на тип); восстановление сбрасывает отсчёт.
type Monitor struct {
	alerts *Alerter
	// checks — проверки; ненулевое время в ответе — известное начало сбоя (последний сигнал
	// воркера), иначе началом считается первый проход, на котором проверка не прошла.
	checks map[string]func(context.Context) (time.Time, error)
	disk   func() (int, error) // заполнение диска, %
	now    func() time.Time
	since  map[string]time.Time // с какого момента сбой
}

// NewMonitor — проверки базы, Redis и воркера; disk — каталог на диске сервера (вложения).
func NewMonitor(alerts *Alerter, db *gorm.DB, rdb *redis.Client, dir string) *Monitor {
	m := &Monitor{alerts: alerts, now: time.Now, since: map[string]time.Time{}}
	m.checks = map[string]func(context.Context) (time.Time, error){
		AlertPostgres: func(ctx context.Context) (time.Time, error) {
			sqlDB, err := db.DB()
			if err != nil {
				return time.Time{}, err
			}
			return time.Time{}, sqlDB.PingContext(ctx)
		},
		AlertRedis: func(ctx context.Context) (time.Time, error) { return time.Time{}, rdb.Ping(ctx).Err() },
		AlertWorker: func(ctx context.Context) (time.Time, error) {
			at, err := rdb.Get(ctx, HeartbeatKey).Time()
			if err == redis.Nil {
				return time.Time{}, fmt.Errorf("нет сигнала воркера")
			}
			if err != nil {
				return time.Time{}, nil // Redis недоступен — это отдельный сбой, о воркере судить нельзя
			}
			if m.now().Sub(at) > HeartbeatInterval*2 {
				// Сигнал запоздал: сбой идёт с последнего сигнала, минута отсчитывается от него.
				return at, fmt.Errorf("последний сигнал воркера в %s UTC", at.UTC().Format("15:04:05"))
			}
			return time.Time{}, nil
		},
	}
	m.disk = func() (int, error) { return diskUsage(dir) }
	return m
}

// Run — проверки каждые 15 секунд, пока жив ctx.
func (m *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(HeartbeatInterval)
	defer t.Stop()
	for {
		m.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick — один проход проверок.
func (m *Monitor) Tick(ctx context.Context) {
	now := m.now()
	for _, kind := range []string{AlertPostgres, AlertRedis, AlertWorker} {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		known, err := m.checks[kind](cctx)
		cancel()
		if err == nil {
			delete(m.since, kind)
			if kind == AlertPostgres {
				if err := m.alerts.Refresh(ctx); err != nil {
					slog.Warn("alert: admins", "err", err)
				}
			}
			continue
		}
		since, ok := m.since[kind]
		if !known.IsZero() {
			since, ok = known, true
		}
		if !ok {
			m.since[kind] = now
			continue
		}
		m.since[kind] = since
		if now.Sub(since) >= downThreshold {
			m.alerts.Notify(ctx, kind, Text("alert_"+kind, map[string]any{"Since": since, "Err": err.Error()}))
		}
	}
	if used, err := m.disk(); err != nil {
		slog.Warn("alert: disk usage", "err", err)
	} else if used > DiskLimit {
		m.alerts.Notify(ctx, AlertDisk, Text("alert_disk", map[string]any{"Used": used}))
	}
}

// diskUsage — заполнение файловой системы каталога dir, %, как у df: занято / (занято +
// доступно непривилегированным), резерв root не считается занятым. Каталога ещё нет (вложений
// не было) — замер по ближайшему существующему родителю на той же файловой системе.
func diskUsage(dir string) (int, error) {
	var st syscall.Statfs_t
	for {
		err := syscall.Statfs(dir, &st)
		if err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if !errors.Is(err, syscall.ENOENT) || parent == dir {
			return 0, err
		}
		dir = parent
	}
	used := st.Blocks - st.Bfree
	if used+st.Bavail == 0 {
		return 0, nil
	}
	return int((used*100 + used + st.Bavail - 1) / (used + st.Bavail)), nil
}

// Heartbeat — сигнал воркера для Monitor: метка времени now() в Redis каждые 15 секунд.
func Heartbeat(ctx context.Context, rdb *redis.Client, now func() time.Time) {
	t := time.NewTicker(HeartbeatInterval)
	defer t.Stop()
	for {
		if err := rdb.Set(ctx, HeartbeatKey, now(), HeartbeatTTL).Err(); err != nil && ctx.Err() == nil {
			slog.Warn("worker: heartbeat", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
