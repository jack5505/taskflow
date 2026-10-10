package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

func get(t *testing.T, s *httpserver.Server, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	s.Router().ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func TestHealthWithoutDatabase(t *testing.T) {
	s := httpserver.New(&config.Config{Env: "test"}, nil, nil)
	rec, body := get(t, s, "/health")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "degraded", body["status"])
	require.NotEmpty(t, rec.Header().Get("X-Request-Id"))
}

func TestHealthWithDatabaseAndRedis(t *testing.T) {
	g := testutil.SharedDB(t)
	rdb := queue.NewRedis(testutil.SharedRedisAddr(t))
	t.Cleanup(func() { _ = rdb.Close() })
	s := httpserver.New(&config.Config{Env: "test"}, g, rdb)

	rec, body := get(t, s, "/health")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, "ok", body["checks"].(map[string]any)["redis"])

	// База ушла — /health должен отвечать 503 без деталей подключения (ТЗ §10.3).
	require.NoError(t, db.Close(g))
	rec, body = get(t, s, "/health")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "degraded", body["status"])
	checks := body["checks"].(map[string]any)
	require.Equal(t, "unavailable", checks["postgres"])
	require.NotContains(t, rec.Body.String(), "taskflow_test", "строка подключения не должна утекать наружу")
}

func TestHealthWithRedisDown(t *testing.T) {
	g := testutil.SharedDB(t)
	rdb := queue.NewRedis("127.0.0.1:1")
	t.Cleanup(func() { _ = rdb.Close() })
	s := httpserver.New(&config.Config{Env: "test"}, g, rdb)

	rec, body := get(t, s, "/health")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	checks := body["checks"].(map[string]any)
	require.Equal(t, "ok", checks["postgres"])
	require.Equal(t, "unavailable", checks["redis"])
}
