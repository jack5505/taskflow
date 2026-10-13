package httpserver_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// Заголовки §10.3 есть в каждом ответе приложения: SPA, API, ошибки, Swagger; обработчик может
// поставить свою CSP строже (как отдача вложений).
func TestSecurityHeaders(t *testing.T) {
	s := httpserver.New(&config.Config{Env: "test"}, nil, nil)
	s.Mount(func(api *gin.RouterGroup) {
		api.GET("/strict", func(c *gin.Context) {
			c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
			c.Status(http.StatusOK)
		})
	})
	r := s.Router()

	for _, p := range []string{"/", "/projects/TM", "/api/v1/ping", "/api/v1/nope", "/api/docs/index.html"} {
		rec, _ := do(t, r, http.MethodGet, p, "")
		h := rec.Header()
		require.Equal(t, "DENY", h.Get("X-Frame-Options"), p)
		require.Equal(t, "nosniff", h.Get("X-Content-Type-Options"), p)
		require.Equal(t, "same-origin", h.Get("Referrer-Policy"), p)
		csp := h.Get("Content-Security-Policy")
		require.Contains(t, csp, "default-src 'self'", p)
		require.Contains(t, csp, "script-src 'self';", p)
		require.Contains(t, csp, "frame-ancestors 'none'", p)
		require.Contains(t, csp, "object-src 'none'", p)
		if p == "/api/docs/index.html" {
			require.Contains(t, csp, "style-src 'self' 'unsafe-inline'", p)
		} else {
			require.Contains(t, csp, "style-src 'self';", p)
		}
	}

	rec, _ := do(t, r, http.MethodGet, "/api/v1/strict", "")
	require.Equal(t, "default-src 'none'; sandbox", rec.Header().Get("Content-Security-Policy"))
	require.Len(t, rec.Header().Values("Content-Security-Policy"), 1)
}
