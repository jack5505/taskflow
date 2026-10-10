package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/db"
)

// healthHandler отвечает 200, когда база доступна, иначе 503 (ТЗ §10.3).
// Текст ошибки наружу не отдаётся: он содержит хост, пользователя и имя базы. Он пишется в лог с request_id.
// Проверка Redis добавится вместе с очередью (issue #2).
//
// @Summary      Состояние сервиса
// @Description  Проверяет PostgreSQL (и Redis, когда появится очередь). 200 — всё доступно, 503 — есть недоступный компонент.
// @Tags         system
// @Produce      json
// @Success      200  {object}  map[string]any  "status: ok"
// @Failure      503  {object}  map[string]any  "status: degraded"
// @Router       /health [get]
func (s *Server) healthHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	checks := gin.H{}
	status := http.StatusOK

	if s.db == nil {
		checks["postgres"] = "not configured"
		status = http.StatusServiceUnavailable
	} else if err := db.Ping(ctx, s.db); err != nil {
		slog.Error("health: postgres", "request_id", c.GetString("request_id"), "err", err)
		checks["postgres"] = "unavailable"
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
