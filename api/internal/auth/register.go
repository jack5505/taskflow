package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Регистрация по ссылке-приглашению (ТЗ §7.1) живёт в модуле project (таблица project_invites).
// Здесь — то, что принадлежит входу: лимит попыток с IP, правила пароля, токены ссылок и сессия
// нового пользователя.

const (
	registerMaxAttempts = 10
	registerWindow      = time.Hour
	registerPrefix      = "register:ip:"
)

var ErrRegisterLimited = apierr.RateLimited("Слишком много попыток регистрации, подождите")

// TakeRegisterAttempt засчитывает попытку регистрации с IP: не больше 10 в час (§7.1). Считается
// каждая попытка, удачная тоже: лимит ограничивает и перебор, и массовое заведение аккаунтов.
func (s *Service) TakeRegisterAttempt(ctx context.Context, ip string) error {
	key := registerPrefix + ip
	pipe := s.lock.rdb.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, registerWindow)
	ttl := pipe.TTL(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("auth: register limit: %w", err)
	}
	if incr.Val() > registerMaxAttempts {
		return ErrRegisterLimited.With("retry_after_seconds", int(ttl.Val().Seconds()))
	}
	return nil
}

// IssueTx выпускает сессию пользователю в транзакции вызывающего: регистрация по ссылке создаёт
// пользователя, участника и сессию одним коммитом.
func (s *Service) IssueTx(tx *gorm.DB, u *user.User, userAgent string) (*Session, error) {
	return s.issueTx(tx, u, userAgent)
}

// NewLinkToken — токен для ссылки: raw идёт в URL, в базу — только hash (как у ссылок пароля).
func NewLinkToken() (raw, hash string, err error) {
	raw, err = newRefreshToken()
	if err != nil {
		return "", "", fmt.Errorf("auth: link token: %w", err)
	}
	return raw, hashToken(raw), nil
}

// HashLinkToken — хеш токена из ссылки для поиска в базе.
func HashLinkToken(raw string) string { return hashToken(raw) }

// ValidatePassword — правила пароля §7.1; нарушение — 400 VALIDATION_FAILED у поля field.
func ValidatePassword(field, password string) error { return validatePassword(field, password) }

// Respond — ответ входа: cookie refresh_token и тело SessionResponse (как у /auth/login).
func (h *Handlers) Respond(c *gin.Context, s *Session) { h.respond(c, s) }
