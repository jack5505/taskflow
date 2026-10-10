package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/events"
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

	require.Eventually(t, func() bool {
		var unsent int64
		require.NoError(t, g.Raw(`SELECT count(*) FROM outbox_events WHERE sent_at IS NULL`).Scan(&unsent).Error)
		return unsent == 0
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
	err := worker.Run(ctx, cfg, g)
	require.Error(t, err, "сервер asynq не стартует без Redis")
}
