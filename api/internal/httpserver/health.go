package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/logx"
	"github.com/jack5505/taskflow/api/internal/queue"
)

// healthHandler отвечает 200, когда база и Redis доступны, иначе 503 (ТЗ §10.3).
// Текст ошибки наружу не отдаётся: он содержит хост, пользователя и имя базы. Он пишется в лог с request_id.
//
// @Summary      Состояние сервиса
// @Description  Проверяет PostgreSQL и Redis. 200 — всё доступно, 503 — есть недоступный компонент.
// @Tags         system
// @Produce      json
// @Success      200  {object}  map[string]any  "status: ok"
// @Failure      503  {object}  map[string]any  "status: degraded"
// @Router       /health [get]
func (s *Server) healthHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	log := logx.From(c.Request.Context())

	checks := gin.H{}
	ok := true

	if s.db == nil {
		checks["postgres"] = "not configured"
		ok = false
	} else if err := db.Ping(ctx, s.db); err != nil {
		log.Error("health: postgres", "err", err)
		checks["postgres"] = "unavailable"
		ok = false
	} else {
		checks["postgres"] = "ok"
	}

	if s.redis == nil {
		checks["redis"] = "not configured"
		ok = false
	} else if err := queue.Ping(ctx, s.redis); err != nil {
		log.Error("health: redis", "err", err)
		checks["redis"] = "unavailable"
		ok = false
	} else {
		checks["redis"] = "ok"
	}

	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "checks": checks})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "checks": checks})
}
