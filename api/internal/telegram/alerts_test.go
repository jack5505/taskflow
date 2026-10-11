package telegram_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// alertAdmin — активный администратор с привязанным Telegram; возвращает его chat id строкой.
func alertAdmin(t *testing.T) (int64, string) {
	t.Helper()
	g := testutil.SharedDB(t)
	u := user.New(testutil.UniqueEmail(t), "x", "Админ")
	u.IsSystemAdmin = true
	tg := rand.Int64N(1<<40) + 1
	u.TelegramUserID = &tg
	require.NoError(t, g.Create(u).Error)
	return tg, strconv.FormatInt(tg, 10)
}

// alertsTo — оповещения, ушедшие в чат chat.
func (e *env) alertsTo(chat string) []string {
	var out []string
	for _, c := range e.tg.Calls("sendMessage") {
		if c.Params["chat_id"] == chat {
			out = append(out, c.Params["text"])
		}
	}
	return out
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func healthy(context.Context) error      { return nil }
func (e *env) monitor(t *testing.T, c *clock) *telegram.Monitor {
	t.Helper()
	g := testutil.SharedDB(t)
	a := telegram.NewAlerter(g, e.api)
	a.SetClock(c.now)
	m := telegram.NewMonitor(a, g, e.rdb, t.TempDir())
	m.SetClock(c.now)
	for _, k := range []string{telegram.AlertPostgres, telegram.AlertRedis, telegram.AlertWorker} {
		m.SetCheck(k, healthy)
	}
	m.SetDisk(func() (int, error) { return 10, nil })
	return m
}

// Критерий #63: Redis недоступен 2 минуты (проверка каждые 15 секунд) — ровно одно оповещение;
// меньше минуты — ни одного; после 10 минут непрерывного сбоя — следующее.
func TestAlertRedisDownOnce(t *testing.T) {
	e := newEnv(t)
	_, chat := alertAdmin(t)
	c := &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	m := e.monitor(t, c)
	ctx := context.Background()

	down := func(context.Context) error { return errors.New("connection refused") }
	m.SetCheck(telegram.AlertRedis, down)
	for range 3 { // 45 секунд — меньше минуты
		m.Tick(ctx)
		c.advance(15 * time.Second)
	}
	m.SetCheck(telegram.AlertRedis, healthy)
	m.Tick(ctx)
	require.Empty(t, e.alertsTo(chat), "сбой короче минуты — без оповещения")

	m.SetCheck(telegram.AlertRedis, down)
	for range 9 { // 2 минуты
		m.Tick(ctx)
		c.advance(15 * time.Second)
	}
	m.SetCheck(telegram.AlertRedis, healthy)
	m.Tick(ctx)
	alerts := e.alertsTo(chat)
	require.Len(t, alerts, 1, "одно оповещение за двухминутный сбой")
	require.Contains(t, alerts[0], "Redis недоступен с 12:00:45 UTC")

	// Новый сбой через 3 минуты — ещё в пределах 10 минут от прошлого оповещения.
	c.advance(3 * time.Minute)
	m.SetCheck(telegram.AlertRedis, down)
	for range 9 {
		m.Tick(ctx)
		c.advance(15 * time.Second)
	}
	require.Len(t, e.alertsTo(chat), 1, "не чаще раза в 10 минут на тип")
	for range 30 { // сбой продолжается ещё 7.5 минут
		m.Tick(ctx)
		c.advance(15 * time.Second)
	}
	require.Len(t, e.alertsTo(chat), 2, "через 10 минут — следующее")
}

// Типы сбоев независимы; диск — не чаще раза в сутки.
func TestAlertKindsAndDisk(t *testing.T) {
	e := newEnv(t)
	_, chat := alertAdmin(t)
	c := &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	m := e.monitor(t, c)
	ctx := context.Background()

	m.SetCheck(telegram.AlertPostgres, func(context.Context) error { return errors.New("dial tcp: refused") })
	m.SetCheck(telegram.AlertWorker, func(context.Context) error { return errors.New("нет сигнала воркера") })
	m.SetDisk(func() (int, error) { return 85, nil })
	for range 6 {
		m.Tick(ctx)
		c.advance(15 * time.Second)
	}
	alerts := e.alertsTo(chat)
	require.Len(t, alerts, 3, "база, воркер и диск — по одному")
	require.Contains(t, alerts[0], "диск сервера заполнен на 85%", "диск — сразу, без минуты ожидания")
	require.Contains(t, alerts[1], "PostgreSQL недоступен")
	require.Contains(t, alerts[2], "воркер не отвечает")

	m.SetCheck(telegram.AlertPostgres, healthy)
	m.SetCheck(telegram.AlertWorker, healthy)
	c.advance(12 * time.Hour)
	m.Tick(ctx)
	require.Len(t, e.alertsTo(chat), 3, "диск — раз в сутки")
	c.advance(12 * time.Hour)
	m.Tick(ctx)
	require.Len(t, e.alertsTo(chat), 4)
}

// Сигнал воркера: нет ключа — сбой с первого прохода; метка опоздала больше чем на два интервала —
// сбой с момента последнего сигнала, оповещение через минуту после него.
func TestAlertWorkerHeartbeat(t *testing.T) {
	e := newEnv(t)
	_, chat := alertAdmin(t)
	g := testutil.SharedDB(t)
	c := &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	monitor := func() *telegram.Monitor {
		a := telegram.NewAlerter(g, e.api)
		a.SetClock(c.now)
		m := telegram.NewMonitor(a, g, e.rdb, t.TempDir())
		m.SetClock(c.now)
		m.SetDisk(func() (int, error) { return 10, nil })
		return m
	}
	ctx := context.Background()

	m := monitor()
	m.Tick(ctx)
	c.advance(time.Minute)
	m.Tick(ctx)
	require.Len(t, e.alertsTo(chat), 1, "воркер не подавал сигнал минуту")
	require.Contains(t, e.alertsTo(chat)[0], "нет сигнала воркера")

	m = monitor() // новый процесс API — свой отсчёт частоты
	hbCtx, stop := context.WithCancel(ctx)
	go telegram.Heartbeat(hbCtx, e.rdb, c.now) // метка — часы теста
	require.Eventually(t, func() bool { return e.rdb.Exists(ctx, telegram.HeartbeatKey).Val() == 1 }, 5*time.Second, 50*time.Millisecond)
	stop()
	last := c.t
	c.advance(20 * time.Second)
	m.Tick(ctx)
	c.advance(25 * time.Second) // 45 с без сигнала: сбой, но меньше минуты
	m.Tick(ctx)
	require.Len(t, e.alertsTo(chat), 1)
	c.advance(20 * time.Second) // 65 с с последнего сигнала
	m.Tick(ctx)
	alerts := e.alertsTo(chat)
	require.Len(t, alerts, 2, "минута отсчитывается от последнего сигнала, а не от первого замеченного сбоя")
	require.Contains(t, alerts[1], "воркер не отвечает с "+last.Format("15:04:05")+" UTC")
}

// Сообщение отброшено после 5 неудачных попыток — оповещение администраторам (§10.3).
func TestAlertOnDroppedMessage(t *testing.T) {
	e := newEnv(t)
	_, chat := alertAdmin(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out).WithAlerts(telegram.NewAlerter(testutil.SharedDB(t), e.api))
	m := telegram.Message{Op: telegram.OpSend, ChatID: 42, Text: "привет", Attempt: telegram.MaxAttempts - 1}
	e.tg.Respond("sendMessage", `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
	require.NoError(t, e.deliver(t, d, m))
	alerts := e.alertsTo(chat)
	require.Len(t, alerts, 1)
	require.Contains(t, alerts[0], "сообщение в чат 42 не отправлено после 5 попыток")
}

// Список администраторов монитор берёт, пока база жива: PostgreSQL падает первым же сбоем после
// старта — оповещение всё равно доходит.
func TestAlertPostgresDownUsesListFromMonitor(t *testing.T) {
	e := newEnv(t)
	g, _ := testutil.TempDB(t)
	u := user.New(testutil.UniqueEmail(t), "x", "Админ")
	u.IsSystemAdmin = true
	tg := rand.Int64N(1<<40) + 1
	u.TelegramUserID = &tg
	require.NoError(t, g.Create(u).Error)
	chat := strconv.FormatInt(tg, 10)

	c := &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	a := telegram.NewAlerter(g, e.api)
	a.SetClock(c.now)
	m := telegram.NewMonitor(a, g, e.rdb, t.TempDir())
	m.SetClock(c.now)
	m.SetCheck(telegram.AlertRedis, healthy)
	m.SetCheck(telegram.AlertWorker, healthy)
	m.SetDisk(func() (int, error) { return 10, nil })
	ctx := context.Background()

	m.Tick(ctx) // база жива — список получателей прочитан
	sqlDB, err := g.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close()) // PostgreSQL недоступен
	for range 6 {
		c.advance(15 * time.Second)
		m.Tick(ctx)
	}
	alerts := e.alertsTo(chat)
	require.Len(t, alerts, 1)
	require.Contains(t, alerts[0], "PostgreSQL недоступен")
}

// Оповещение, которое никому не дошло (нет получателей, Telegram отказал), не занимает период:
// следующая попытка уходит сразу.
func TestAlertUndeliveredDoesNotSilence(t *testing.T) {
	e := newEnv(t)
	g, _ := testutil.TempDB(t)
	a := telegram.NewAlerter(g, e.api)
	ctx := context.Background()
	require.False(t, a.Notify(ctx, telegram.AlertDisk, "диск"), "получателей нет")

	u := user.New(testutil.UniqueEmail(t), "x", "Админ")
	u.IsSystemAdmin = true
	tg := rand.Int64N(1<<40) + 1
	u.TelegramUserID = &tg
	require.NoError(t, g.Create(u).Error)
	chat := strconv.FormatInt(tg, 10)
	e.tg.Respond("sendMessage", `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
	require.False(t, a.Notify(ctx, telegram.AlertDisk, "диск"), "Telegram отказал")
	require.True(t, a.Notify(ctx, telegram.AlertDisk, "диск"))
	require.False(t, a.Notify(ctx, telegram.AlertDisk, "диск"), "дошло — следующее через сутки")
	require.Len(t, e.alertsTo(chat), 2, "попытка с отказом и успешная")

	require.True(t, telegram.NewAlerter(g, nil).Notify(ctx, telegram.AlertRedis, "без бота — только лог"))
}

// Диск: каталога вложений ещё нет — замер по существующему родителю, как у df.
func TestAlertDiskUsage(t *testing.T) {
	used, err := telegram.DiskUsage(t.TempDir() + "/attachments/not/yet")
	require.NoError(t, err)
	require.True(t, used >= 0 && used <= 100, used)
}
