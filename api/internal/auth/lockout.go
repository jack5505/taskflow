package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Блокировка входа (ТЗ §10.3): после maxFailures неудачных попыток по одному email
// вход закрыт на lockoutWindow, даже с верным паролем. Счётчик живёт в Redis и
// сбрасывается успешным входом. Те же правила — у ввода текущего пароля при его смене
// в профиле, но со своим счётчиком по id пользователя (см. ChangePassword).
const (
	maxFailures   = 10
	lockoutWindow = 15 * time.Minute
)

// lockout — счётчик неудач с префиксом ключа: loginFailPrefix (по email) или
// pwchangeFailPrefix (по id пользователя).
type lockout struct {
	rdb    *redis.Client
	prefix string
}

const (
	loginFailPrefix    = "login:fail:"
	pwchangeFailPrefix = "pwchange:fail:"
)

func (l lockout) key(id string) string { return l.prefix + strings.ToLower(id) }

// locked сообщает, закрыт ли вход, и сколько осталось ждать.
func (l lockout) locked(ctx context.Context, id string) (bool, time.Duration, error) {
	n, err := l.rdb.Get(ctx, l.key(id)).Int()
	if err == redis.Nil {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("lockout get: %w", err)
	}
	if n < maxFailures {
		return false, 0, nil
	}
	ttl, err := l.rdb.TTL(ctx, l.key(id)).Result()
	if err != nil {
		return false, 0, fmt.Errorf("lockout ttl: %w", err)
	}
	switch ttl {
	case -2:
		// Ключ истёк или пропал между GET и TTL — блокировки уже нет.
		return false, 0, nil
	case -1:
		// Ключ есть, но без срока (EXPIRE не применился) — не блокировать навечно: взводим срок заново.
		if err := l.rdb.Expire(ctx, l.key(id), lockoutWindow).Err(); err != nil {
			return false, 0, fmt.Errorf("lockout re-expire: %w", err)
		}
		ttl = lockoutWindow
	}
	return true, ttl, nil
}

// fail засчитывает неудачную попытку; окно отсчитывается от первой неудачи.
func (l lockout) fail(ctx context.Context, id string) error {
	pipe := l.rdb.TxPipeline()
	incr := pipe.Incr(ctx, l.key(id))
	pipe.ExpireNX(ctx, l.key(id), lockoutWindow)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("lockout incr: %w", err)
	}
	if incr.Val() >= maxFailures {
		// С этого момента блокировка держится ровно lockoutWindow.
		if err := l.rdb.Expire(ctx, l.key(id), lockoutWindow).Err(); err != nil {
			return fmt.Errorf("lockout expire: %w", err)
		}
	}
	return nil
}

func (l lockout) reset(ctx context.Context, id string) error {
	return l.rdb.Del(ctx, l.key(id)).Err()
}
