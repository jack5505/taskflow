package httpserver

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// Политики Content-Security-Policy (ТЗ §10.3). Веб-интерфейс — сборка Vite: скрипты и стили только
// файлами со своего адреса, без inline. Картинки — свои (вложения по подписанной ссылке), data: и
// blob: (выгрузка CSV). Swagger UI рисует inline-стили и data:-иконки, поэтому у /api/docs своя,
// более мягкая политика по стилям; скрипты и там только свои.
const (
	cspApp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
		"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
	cspDocs = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
)

// securityHeaders ставит заголовки безопасности (ТЗ §10.3) в каждый ответ приложения — и без nginx
// (127.0.0.1:8100, e2e). Обработчик может заменить CSP своей, строже: так делает отдача вложений.
// HSTS ставит nginx: только он знает, что снаружи HTTPS.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		csp := cspApp
		if p := c.Request.URL.Path; p == "/api/docs" || strings.HasPrefix(p, "/api/docs/") {
			csp = cspDocs
		}
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		c.Next()
	}
}
