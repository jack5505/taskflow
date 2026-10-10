package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

const headerRequestID = "X-Request-Id"

// requestID ставит X-Request-Id в каждый ответ (ТЗ §9.3) и кладёт его в контекст для логов.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerRequestID)
		if id == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		c.Set("request_id", id)
		c.Header(headerRequestID, id)
		c.Next()
	}
}

// accessLog — структурированный JSON-лог запроса с request_id (ТЗ §10.3).
func accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("http",
			"request_id", c.GetString("request_id"),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}
