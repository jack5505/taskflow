package httpserver_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func TestRunServesAndShutsDown(t *testing.T) {
	addr := freePort(t)
	s := httpserver.New(&config.Config{Env: "test", Addr: addr}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	var resp *http.Response
	require.Eventually(t, func() bool {
		r, err := http.Get("http://" + addr + "/health")
		if err != nil {
			return false
		}
		resp = r
		return true
	}, 5*time.Second, 50*time.Millisecond)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "без базы и Redis — 503")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
}

func TestRunFailsOnBusyPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	// Env=production переключает глобальный режим Gin — возвращаем тестовый после теста.
	t.Cleanup(func() { gin.SetMode(gin.TestMode) })
	s := httpserver.New(&config.Config{Env: "production", Addr: l.Addr().String()}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.Error(t, s.Run(ctx))
}

func TestSPAServesIndexAndAssets(t *testing.T) {
	s := httpserver.New(&config.Config{Env: "test"}, nil, nil)
	rec, _ := do(t, s.Router(), http.MethodGet, "/", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")

	// Глубокая ссылка SPA отдаёт index.html — маршрутизация на стороне React.
	rec, _ = do(t, s.Router(), http.MethodGet, "/projects/TM/board", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")

	// Существующий файл идёт через http.FileServer: index.html он канонизирует редиректом на /.
	rec, _ = do(t, s.Router(), http.MethodGet, "/index.html", "")
	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	require.Equal(t, "./", rec.Header().Get("Location"))
}
