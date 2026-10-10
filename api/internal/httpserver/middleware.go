package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/logx"
)

const (
	headerRequestID = "X-Request-Id"
	ctxRequestID    = "request_id"
)

// requestID ставит X-Request-Id в каждый ответ (ТЗ §9.3), кладёт его в контекст Gin
// и привязывает к запросу логгер с этим полем — все строки лога по запросу его содержат.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerRequestID)
		if id == "" || len(id) > 64 {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		c.Set(ctxRequestID, id)
		c.Header(headerRequestID, id)
		c.Request = c.Request.WithContext(logx.With(c.Request.Context(), slog.Default().With(ctxRequestID, id)))
		c.Next()
	}
}

// RequestID возвращает идентификатор текущего запроса.
func RequestID(c *gin.Context) string { return c.GetString(ctxRequestID) }

// accessLog — структурированный JSON-лог запроса с request_id (ТЗ §10.3).
func accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if code, ok := c.Get("error_code"); ok {
			attrs = append(attrs, "error_code", code)
		}
		logx.From(c.Request.Context()).Info("http", attrs...)
	}
}

// recovery превращает панику в 500 по формату §9.3 и пишет стек в лог.
func recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, rec any) {
		logx.From(c.Request.Context()).Error("panic", "panic", rec, "stack", string(debug.Stack()))
		writeError(c, apierr.Internal(errors.New("panic")))
	})
}

// Abort пишет ошибку в формате §9.3 и прерывает цепочку обработчиков.
// Доменные *apierr.Error уходят как есть; всё остальное — 500 с записью причины в лог.
func Abort(c *gin.Context, err error) {
	e := apierr.From(err)
	if e.Status >= http.StatusInternalServerError {
		logx.From(c.Request.Context()).Error("request failed", "code", e.Code, "err", err)
	}
	writeError(c, e)
}

// Bind разбирает JSON-тело по binding-тегам; при ошибке отвечает 400 и возвращает false.
func Bind(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		writeError(c, apierr.FromBinding(err))
		return false
	}
	return true
}

func writeError(c *gin.Context, e *apierr.Error) {
	details := map[string]any{ctxRequestID: RequestID(c)}
	for k, v := range e.Details {
		details[k] = v
	}
	c.Set("error_code", e.Code)
	c.AbortWithStatusJSON(e.Status, apierr.Response{Error: apierr.Body{Code: e.Code, Message: e.Message, Details: details}})
}
