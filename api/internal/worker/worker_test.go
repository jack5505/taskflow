package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/worker"
)

// Воркер целиком: событие из outbox уходит в очередь и помечается отправленным,
// по отмене контекста Run возвращается.
func TestRunDeliversEventsAndStopsOnCancel(t *testing.T) {
	g := testutil.SharedDB(t)
	cfg := &config.Config{RedisAddr: testutil.SharedRedisAddr(t)}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, cfg, g) }()

	require.NoError(t, g.Transaction(func(tx *gorm.DB) error {
		return events.Publish(tx, events.ProjectArchived, map[string]any{"key": "TM"})
	}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var unsent int64
		if !assert.NoError(c, g.Raw(`SELECT count(*) FROM outbox_events WHERE sent_at IS NULL`).Scan(&unsent).Error) {
			return
		}
		assert.Zero(c, unsent)
	}, 10*time.Second, 100*time.Millisecond, "релей воркера должен отправить событие")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("worker.Run не завершился после отмены контекста")
	}
}

func TestRunFailsFastWithoutRedis(t *testing.T) {
	g := testutil.SharedDB(t)
	cfg := &config.Config{RedisAddr: "127.0.0.1:1"}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	err := worker.Run(ctx, cfg, g)
	require.Error(t, err, "воркер без Redis должен завершиться с ошибкой")
	require.Less(t, time.Since(start), 15*time.Second)
}

// SIGTERM во время стартовой проверки Redis — штатная остановка, а не ошибка.
func TestRunStopsCleanlyWhenCancelledDuringPing(t *testing.T) {
	g := testutil.SharedDB(t)
	cfg := &config.Config{RedisAddr: "10.255.255.1:6379"} // пакеты уходят в никуда, ping висит до таймаута

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, cfg, g) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
}

// С токеном бота воркер берёт задания бота: сообщение из очереди уходит в Bot API.
func TestRunSendsTelegramMessages(t *testing.T) {
	g := testutil.SharedDB(t)
	tg := testutil.NewFakeTelegram(t)
	cfg := &config.Config{RedisAddr: testutil.SharedRedisAddr(t), TelegramBotToken: "123:TEST", TelegramAPIURL: tg.URL}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, cfg, g) }()

	jobs := queue.NewClient(cfg.RedisAddr)
	t.Cleanup(func() { _ = jobs.Close() })
	require.NoError(t, telegram.NewQueue(jobs).Send(context.Background(), telegram.Message{Op: telegram.OpSend, ChatID: 42, Text: "привет"}, 0))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, tg.Calls("sendMessage"), 1)
	}, 10*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("worker.Run не завершился после отмены контекста")
	}
}
