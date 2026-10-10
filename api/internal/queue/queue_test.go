package queue_test

import (
	"context"
	"sync/atomic"
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

func TestServerProcessesTaskAndStops(t *testing.T) {
	addr := testutil.SharedRedisAddr(t)
	var processed atomic.Int32
	srv := queue.NewServer(addr, 1)
	mux := asynq.NewServeMux()
	mux.HandleFunc("noop", func(context.Context, *asynq.Task) error { processed.Add(1); return nil })
	require.NoError(t, srv.Start(mux))
	t.Cleanup(srv.Shutdown)

	client := queue.NewClient(addr)
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.Enqueue(asynq.NewTask("noop", nil), asynq.Queue(queue.QueueLow))
	require.NoError(t, err)

	// Дожидаемся обработки: иначе задание останется в общем Redis и всплывёт в другом тесте.
	require.Eventually(t, func() bool { return processed.Load() == 1 }, 10*time.Second, 50*time.Millisecond)
}

func TestFailedTaskAndAsynqLogsGoThroughSlog(t *testing.T) {
	logs := testutil.CaptureLogs(t)

	addr := testutil.SharedRedisAddr(t)
	srv := queue.NewServer(addr, 1)
	mux := asynq.NewServeMux()
	mux.HandleFunc("fail", func(context.Context, *asynq.Task) error { return asynq.SkipRetry })
	require.NoError(t, srv.Start(mux))

	client := queue.NewClient(addr)
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.Enqueue(asynq.NewTask("fail", nil))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return logs.Contains(`"msg":"asynq task failed"`) && logs.Contains(`"type":"fail"`)
	}, 10*time.Second, 100*time.Millisecond)

	srv.Shutdown()
	require.Contains(t, logs.String(), "shutdown", "логи asynq идут через slog")
}
