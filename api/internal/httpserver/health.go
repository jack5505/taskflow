package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/db"
)

// healthHandler отвечает 200, когда база доступна, иначе 503 (ТЗ §10.3).
// Проверка Redis добавится вместе с очередью (issue #2).
func (s *Server) healthHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	checks := gin.H{}
	status := http.StatusOK

	if s.db == nil {
		checks["postgres"] = "not configured"
		status = http.StatusServiceUnavailable
	} else if err := db.Ping(ctx, s.db); err != nil {
		checks["postgres"] = "error: " + err.Error()
		status = http.StatusServiceUnavailable
	} else {
		checks["postgres"] = "ok"
	}

	body := gin.H{"status": "ok", "checks": checks}
	if status != http.StatusOK {
		body["status"] = "degraded"
	}
	c.JSON(status, body)
}
