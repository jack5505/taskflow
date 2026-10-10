package httpserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// Swagger UI и спецификация доступны по /api/docs (ТЗ §10.3) и описывают все маршруты.
func TestSwaggerDocsAreServed(t *testing.T) {
	r := httpserver.New(&config.Config{Env: "test"}, nil, nil).Router()

	for _, p := range []string{"/api/docs", "/api/docs/"} {
		rec, _ := do(t, r, http.MethodGet, p, "")
		require.Equal(t, http.StatusFound, rec.Code, p)
		require.Equal(t, "/api/docs/index.html", rec.Header().Get("Location"), p)
	}

	// Неизвестный путь и чужой метод под /api/docs — в формате §9.3, а не plain-text от gin-swagger.
	rec, resp := do(t, r, http.MethodGet, "/api/docs/whatever", "")
	requireFormat(t, rec, resp, http.StatusNotFound, apierr.CodeNotFound)
	rec, resp = do(t, r, http.MethodPost, "/api/docs/index.html", "")
	requireFormat(t, rec, resp, http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed)

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
