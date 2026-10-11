package task

import (
	"time"

	"github.com/google/uuid"
)

// SignedDownloadURL — ссылка на скачивание с произвольным сроком (тест просроченной ссылки).
func SignedDownloadURL(secret string, id uuid.UUID, exp time.Time) string {
	return (&Service{}).WithAttachments("", 0, secret).files.url(id, exp)
}
