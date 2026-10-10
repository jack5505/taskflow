package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/outbox"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

type captured struct {
	mu   sync.Mutex
	envs []events.Envelope
}

func (c *captured) add(env events.Envelope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.envs = append(c.envs, env)
}

func (c *captured) wait(t *testing.T, n int) []events.Envelope {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		if len(c.envs) >= n {
			out := append([]events.Envelope{}, c.envs...)
			c.mu.Unlock()
			return out
		}
		c.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("за 15 с получено меньше %d событий", n)
	return nil
}

// startWorker поднимает asynq-сервер с диспетчером, который складывает события в captured.
func startWorker(t *testing.T, redisAddr string) *captured {
	t.Helper()
	c := &captured{}
	d := events.NewDispatcher(queue.NewRedis(redisAddr))
	d.On("*", "capture", func(_ context.Context, env events.Envelope) error { c.add(env); return nil })
	srv := queue.NewServer(redisAddr, 2)
	require.NoError(t, srv.Start(d.Mux()))
	t.Cleanup(srv.Shutdown)
	return c
}

func unsentCount(t *testing.T, g *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM outbox_events WHERE sent_at IS NULL`).Scan(&n).Error)
	return n
}

func TestEventTravelsOutboxToHandler(t *testing.T) {
	g := testutil.SharedDB(t)
	redisAddr := testutil.SharedRedisAddr(t)
	got := startWorker(t, redisAddr)

	// Событие пишется в той же транзакции, что и данные (ТЗ §9.4).
	require.NoError(t, g.Transaction(func(tx *gorm.DB) error {
		return events.Publish(tx, events.TaskCreated, map[string]any{"task_key": "TM-1"})
	}))
	require.EqualValues(t, 1, unsentCount(t, g))

	client := queue.NewClient(redisAddr)
	t.Cleanup(func() { _ = client.Close() })
	relay := outbox.NewRelay(g, client)

	n, err := relay.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.EqualValues(t, 0, unsentCount(t, g))

	envs := got.wait(t, 1)
	require.Equal(t, events.TaskCreated, envs[0].Type)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(envs[0].Payload, &payload))
	require.Equal(t, "TM-1", payload["task_key"])

	// Повторный проход ничего не шлёт.
	n, err = relay.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestEventSurvivesRedisOutage(t *testing.T) {
	g := testutil.SharedDB(t)
	redisAddr := testutil.SharedRedisAddr(t)

	require.NoError(t, g.Transaction(func(tx *gorm.DB) error {
		return events.Publish(tx, events.SprintClosed, map[string]any{"sprint": 7})
	}))

	// Redis «недоступен»: клиент смотрит на закрытый порт. Событие остаётся в outbox.
	broken := queue.NewClient("127.0.0.1:1")
	t.Cleanup(func() { _ = broken.Close() })
	n, err := outbox.NewRelay(g, broken).RunOnce(context.Background())
	require.Error(t, err)
	require.Zero(t, n)
	require.EqualValues(t, 1, unsentCount(t, g))

	// Redis вернулся — событие доставлено.
	got := startWorker(t, redisAddr)
	client := queue.NewClient(redisAddr)
	t.Cleanup(func() { _ = client.Close() })
	n, err = outbox.NewRelay(g, client).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	envs := got.wait(t, 1)
	require.Equal(t, events.SprintClosed, envs[0].Type)
}

func TestRelayIsIdempotentOnDuplicateTaskID(t *testing.T) {
	g := testutil.SharedDB(t)
	redisAddr := testutil.SharedRedisAddr(t)
	client := queue.NewClient(redisAddr)
	t.Cleanup(func() { _ = client.Close() })

	var ev events.OutboxEvent
	require.NoError(t, g.Transaction(func(tx *gorm.DB) error {
		return events.Publish(tx, events.TaskDeleted, nil)
	}))
	require.NoError(t, g.Where("type = ? AND sent_at IS NULL", events.TaskDeleted).Order("created_at desc").First(&ev).Error)

	// Имитируем сбой между Enqueue и коммитом: задание уже в Redis с тем же TaskID.
	env, _ := json.Marshal(events.Envelope{EventID: ev.ID.String(), Type: ev.Type, Payload: json.RawMessage(ev.Payload)})
	_, err := client.Enqueue(asynq.NewTask(events.TaskTypePrefix+ev.Type, env), asynq.TaskID(ev.ID.String()), asynq.Retention(time.Minute))
	require.NoError(t, err)

	n, err := outbox.NewRelay(g, client).RunOnce(context.Background())
	require.NoError(t, err, "конфликт TaskID не должен считаться ошибкой")
	require.Equal(t, 1, n)
	require.EqualValues(t, 0, unsentCount(t, g))
}

func TestCleanupDeletesOldSentEvents(t *testing.T) {
	g := testutil.SharedDB(t)
	require.NoError(t, g.Exec(`INSERT INTO outbox_events (type, payload, created_at, sent_at)
		VALUES ('x.old', '{}', now() - interval '8 days', now() - interval '8 days'),
		       ('x.new', '{}', now(), now())`).Error)
	n, err := outbox.NewRelay(g, nil).Cleanup(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1))
	var left int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM outbox_events WHERE type IN ('x.old','x.new')`).Scan(&left).Error)
	require.EqualValues(t, 1, left)
}

func TestPublishRejectsUnknownType(t *testing.T) {
	g := testutil.SharedDB(t)
	for _, typ := range []string{"", "task.craeted"} {
		err := g.Transaction(func(tx *gorm.DB) error { return events.Publish(tx, typ, nil) })
		require.Error(t, err, typ)
	}
}

// Обработчик, который уже отработал событие, не вызывается при повторе задания,
// даже если другой обработчик того же события упал.
func TestSucceededHandlerIsNotRerunOnRetry(t *testing.T) {
	g := testutil.SharedDB(t)
	redisAddr := testutil.SharedRedisAddr(t)

	var telegramCalls, emailCalls atomic.Int32
	d := events.NewDispatcher(queue.NewRedis(redisAddr))
	d.On(events.TaskAssigned, "telegram", func(context.Context, events.Envelope) error {
		telegramCalls.Add(1)
		return nil
	})
	d.On(events.TaskAssigned, "email", func(context.Context, events.Envelope) error {
		if emailCalls.Add(1) == 1 {
			return errors.New("smtp временно недоступен")
		}
		return nil
	})
	srv := asynq.NewServer(queue.Opts(redisAddr), asynq.Config{
		Concurrency: 1,
		Queues:      map[string]int{queue.QueueDefault: 1},
		// Повтор почти сразу, иначе тест ждал бы стандартную задержку asynq.
		RetryDelayFunc: func(int, error, *asynq.Task) time.Duration { return 200 * time.Millisecond },
	})
	require.NoError(t, srv.Start(d.Mux()))
	t.Cleanup(srv.Shutdown)

	require.NoError(t, g.Transaction(func(tx *gorm.DB) error {
		return events.Publish(tx, events.TaskAssigned, map[string]any{"task_key": "TM-9"})
	}))
	client := queue.NewClient(redisAddr)
	t.Cleanup(func() { _ = client.Close() })
	_, err := outbox.NewRelay(g, client).RunOnce(context.Background())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return emailCalls.Load() >= 2 }, 15*time.Second, 50*time.Millisecond)
	require.EqualValues(t, 1, telegramCalls.Load(), "успешный обработчик не должен запускаться повторно")
}
