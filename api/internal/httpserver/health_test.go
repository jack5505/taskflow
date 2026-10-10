package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/httpserver"
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
	s := httpserver.New(&config.Config{Env: "test"}, nil)
	rec, body := get(t, s, "/health")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "degraded", body["status"])
	require.NotEmpty(t, rec.Header().Get("X-Request-Id"))
}

func TestHealthWithDatabase(t *testing.T) {
	dsn := testutil.StartPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	g, err := db.Open(ctx, dsn)
	require.NoError(t, err)

	s := httpserver.New(&config.Config{Env: "test"}, g)

	rec, body := get(t, s, "/health")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok", body["status"])

	// База ушла — /health должен отвечать 503 (ТЗ §10.3).
	require.NoError(t, db.Close(g))
	rec, body = get(t, s, "/health")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "degraded", body["status"])
}

func TestAPINotFoundIsJSON(t *testing.T) {
	s := httpserver.New(&config.Config{Env: "test"}, nil)
	rec, body := get(t, s, "/api/v1/nope")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.NotNil(t, body["error"])
}
