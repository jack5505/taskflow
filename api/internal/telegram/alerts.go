package telegram

import (
	"context"
	"fmt"
	"log/slog"
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
// Возвращает, ушло ли оповещение.
func (a *Alerter) Notify(ctx context.Context, kind, text string) bool {
	a.mu.Lock()
	period := alertPeriod
	if kind == AlertDisk {
		period = diskAlertPeriod
	}
	now := a.now()
	if last, ok := a.last[kind]; ok && now.Sub(last) < period {
		a.mu.Unlock()
		return false
	}
	a.last[kind] = now
	a.mu.Unlock()

	slog.Error("alert", "kind", kind, "text", text)
	for _, chat := range a.recipients(ctx) {
		sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		_, err := a.api.SendMessage(sendCtx, &bot.SendMessageParams{ChatID: chat, Text: text})
		cancel()
		if err != nil {
			slog.Error("alert: send", "kind", kind, "chat_id", chat, "err", err)
		}
	}
	return true
}

// recipients — Telegram активных администраторов; база недоступна — последний известный список.
func (a *Alerter) recipients(ctx context.Context) []int64 {
	var ids []int64
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := a.db.WithContext(qctx).Raw(`SELECT telegram_user_id FROM users
		WHERE is_system_admin AND is_active AND telegram_user_id IS NOT NULL ORDER BY id`).Scan(&ids).Error
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		slog.Warn("alert: admins from cache", "err", err)
		return a.admins
	}
	a.admins = ids
	return ids
}

// Monitor — проверки §10.3 в процессе API: PostgreSQL, Redis, сигнал воркера, диск. Сбой длиннее
// минуты — оповещение (не чаще раза в 10 минут на тип); восстановление сбрасывает отсчёт.
type Monitor struct {
	alerts *Alerter
	checks map[string]func(context.Context) error
	disk   func() (int, error) // заполнение диска, %
	now    func() time.Time
	since  map[string]time.Time // с какого момента сбой
}

// NewMonitor — проверки базы, Redis и воркера; disk — каталог на диске сервера (вложения).
func NewMonitor(alerts *Alerter, db *gorm.DB, rdb *redis.Client, dir string) *Monitor {
	m := &Monitor{alerts: alerts, now: time.Now, since: map[string]time.Time{}}
	m.checks = map[string]func(context.Context) error{
		AlertPostgres: func(ctx context.Context) error {
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			return sqlDB.PingContext(ctx)
		},
		AlertRedis: func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		AlertWorker: func(ctx context.Context) error {
			at, err := rdb.Get(ctx, HeartbeatKey).Time()
			if err == redis.Nil {
				return fmt.Errorf("нет сигнала воркера")
			}
			if err != nil {
				return nil // Redis недоступен — это отдельный сбой, о воркере судить нельзя
			}
			if age := m.now().Sub(at); age > downThreshold {
				return fmt.Errorf("последний сигнал воркера %s назад", age.Round(time.Second))
			}
			return nil
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
		err := m.checks[kind](cctx)
		cancel()
		if err == nil {
			delete(m.since, kind)
			continue
		}
		since, ok := m.since[kind]
		if !ok {
			m.since[kind] = now
			continue
		}
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

// diskUsage — заполнение файловой системы каталога dir, %.
func diskUsage(dir string) (int, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	total := st.Blocks * uint64(st.Bsize)
	if total == 0 {
		return 0, nil
	}
	free := st.Bavail * uint64(st.Bsize)
	return int((total - free) * 100 / total), nil
}

// Heartbeat — сигнал воркера для Monitor: метка времени в Redis каждые 15 секунд.
func Heartbeat(ctx context.Context, rdb *redis.Client) {
	t := time.NewTicker(HeartbeatInterval)
	defer t.Stop()
	for {
		if err := rdb.Set(ctx, HeartbeatKey, time.Now(), HeartbeatTTL).Err(); err != nil && ctx.Err() == nil {
			slog.Warn("worker: heartbeat", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
