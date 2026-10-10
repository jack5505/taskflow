package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/outbox"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

// Цикл Run: отправляет накопившееся, переживает отказ Redis с backoff и завершается по отмене.
func TestRunLoopDeliversAndRecovers(t *testing.T) {
	g := testutil.SharedDB(t)
	redisAddr := testutil.SharedRedisAddr(t)
	got := startWorker(t, redisAddr)

	client := queue.NewClient(redisAddr)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { outbox.NewRelay(g, client).Run(ctx); close(done) }()

	require.NoError(t, g.Transaction(func(tx *gorm.DB) error {
		return events.Publish(tx, events.WorklogCreated, map[string]any{"minutes": 30})
	}))
	envs := got.wait(t, 1)
	require.Equal(t, events.WorklogCreated, envs[0].Type)

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
}

func TestRunLoopBacksOffWhenRedisDown(t *testing.T) {
	g := testutil.SharedDB(t)
	broken := queue.NewClient("127.0.0.1:1")
	t.Cleanup(func() { _ = broken.Close() })

	require.NoError(t, g.Transaction(func(tx *gorm.DB) error {
		return events.Publish(tx, events.CommentCreated, nil)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { outbox.NewRelay(g, broken).Run(ctx); close(done) }()
	<-done

	var unsent int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM outbox_events WHERE sent_at IS NULL AND type = ?`, events.CommentCreated).Scan(&unsent).Error)
	require.EqualValues(t, 1, unsent, "событие ждёт восстановления Redis")
	require.NoError(t, g.Exec(`DELETE FROM outbox_events WHERE type = ?`, events.CommentCreated).Error)
}
