package httpserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// Swagger UI и спецификация доступны по /api/docs (ТЗ §10.3) и описывают все маршруты.
func TestSwaggerDocsAreServed(t *testing.T) {
	r := httpserver.New(&config.Config{Env: "test"}, nil, nil).Router()

	rec, _ := do(t, r, http.MethodGet, "/api/docs", "")
	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	require.Equal(t, "/api/docs/index.html", rec.Header().Get("Location"))

	rec, _ = do(t, r, http.MethodGet, "/api/docs/index.html", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "swagger-ui")

	rec, _ = do(t, r, http.MethodGet, "/api/docs/doc.json", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var spec struct {
		Swagger  string                    `json:"swagger"`
		BasePath string                    `json:"basePath"`
		Paths    map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &spec))
	require.Equal(t, "2.0", spec.Swagger)
	require.Equal(t, "/", spec.BasePath)
	require.Contains(t, spec.Paths, "/api/v1/ping")
	require.Contains(t, spec.Paths, "/health")
}
