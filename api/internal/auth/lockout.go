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
// сбрасывается успешным входом.
const (
	maxFailures   = 10
	lockoutWindow = 15 * time.Minute
)

type lockout struct{ rdb *redis.Client }

func (l lockout) key(email string) string { return "login:fail:" + strings.ToLower(email) }

// locked сообщает, закрыт ли вход, и сколько осталось ждать.
func (l lockout) locked(ctx context.Context, email string) (bool, time.Duration, error) {
	n, err := l.rdb.Get(ctx, l.key(email)).Int()
	if err == redis.Nil {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("lockout get: %w", err)
	}
	if n < maxFailures {
		return false, 0, nil
	}
	ttl, err := l.rdb.TTL(ctx, l.key(email)).Result()
	if err != nil {
		return false, 0, fmt.Errorf("lockout ttl: %w", err)
	}
	return true, ttl, nil
}

// fail засчитывает неудачную попытку; окно отсчитывается от первой неудачи.
func (l lockout) fail(ctx context.Context, email string) error {
	pipe := l.rdb.TxPipeline()
	incr := pipe.Incr(ctx, l.key(email))
	pipe.ExpireNX(ctx, l.key(email), lockoutWindow)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("lockout incr: %w", err)
	}
	if incr.Val() >= maxFailures {
		// С этого момента блокировка держится ровно lockoutWindow.
		if err := l.rdb.Expire(ctx, l.key(email), lockoutWindow).Err(); err != nil {
			return fmt.Errorf("lockout expire: %w", err)
		}
	}
	return nil
}

func (l lockout) reset(ctx context.Context, email string) error {
	return l.rdb.Del(ctx, l.key(email)).Err()
}
