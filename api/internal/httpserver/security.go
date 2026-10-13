package httpserver

import (
	"github.com/gin-gonic/gin"
)

// Политики Content-Security-Policy (ТЗ §10.3). Веб-интерфейс — сборка Vite: скрипты и стили только
// файлами со своего адреса, без inline. Картинки — свои (вложения по подписанной ссылке), data: и
// blob: (выгрузка CSV). У Swagger UI (/api/docs, ставит docsHandler) один inline-атрибут style у
// SVG-спрайта в index.html — разрешён по хешу, а не 'unsafe-inline'; иконки — data:.
const (
	cspApp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
		"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
	cspDocs = "default-src 'self'; script-src 'self'; " +
		"style-src 'self' 'unsafe-hashes' 'sha256-ezdv1bOGcoOD7FKudKN0Y2Mb763O6qVtM8LT2mtanIU='; img-src 'self' data:; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
)

// securityHeaders ставит заголовки безопасности (ТЗ §10.3) в каждый ответ приложения — и без nginx
// (127.0.0.1:8100, e2e). Обработчик может заменить CSP своей, строже: так делает отдача вложений.
// HSTS ставит nginx: только он знает, что снаружи HTTPS; остальные заголовки — только здесь.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", cspApp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		c.Next()
	}
}
