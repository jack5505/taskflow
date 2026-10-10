package queue_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

func TestOptsHaveTimeouts(t *testing.T) {
	o := queue.Opts("redis:6379")
	require.Equal(t, "redis:6379", o.Addr)
	require.Equal(t, 5*time.Second, o.DialTimeout)
	require.Equal(t, 5*time.Second, o.ReadTimeout)
	require.Equal(t, 5*time.Second, o.WriteTimeout)
}

func TestPingAgainstRealRedisAndClosedPort(t *testing.T) {
	addr := testutil.SharedRedisAddr(t)
	rdb := queue.NewRedis(addr)
	t.Cleanup(func() { _ = rdb.Close() })
	require.NoError(t, queue.Ping(context.Background(), rdb))

	down := queue.NewRedis("127.0.0.1:1")
	t.Cleanup(func() { _ = down.Close() })
	err := queue.Ping(context.Background(), down)
	require.ErrorContains(t, err, "redis ping")
}

func TestServerStartsAndStops(t *testing.T) {
	addr := testutil.SharedRedisAddr(t)
	srv := queue.NewServer(addr, 1)
	mux := asynq.NewServeMux()
	mux.HandleFunc("noop", func(context.Context, *asynq.Task) error { return nil })
	require.NoError(t, srv.Start(mux))

	client := queue.NewClient(addr)
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.Enqueue(asynq.NewTask("noop", nil), asynq.Queue(queue.QueueLow))
	require.NoError(t, err)

	srv.Shutdown()
}

func TestSlogAdapterWritesToDefaultLogger(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Адаптер — внутренний тип; проверяем через ErrorHandler сервера и логгер asynq косвенно:
	// падающее задание пишет строку "asynq task failed".
	addr := testutil.SharedRedisAddr(t)
	srv := queue.NewServer(addr, 1)
	mux := asynq.NewServeMux()
	mux.HandleFunc("fail", func(context.Context, *asynq.Task) error { return asynq.SkipRetry })
	require.NoError(t, srv.Start(mux))
	client := queue.NewClient(addr)
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.Enqueue(asynq.NewTask("fail", nil))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return bytes.Contains(buf.Bytes(), []byte("asynq task failed")) }, 10*time.Second, 100*time.Millisecond)
	srv.Shutdown()
	require.Contains(t, buf.String(), "shutdown", "логи asynq идут через slog")
}
