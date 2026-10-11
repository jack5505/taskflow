package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Блокировка входа (ТЗ §10.3): после maxFailures неудачных попыток по одному email
// вход закрыт на lockoutWindow, даже с верным паролем. Счётчик живёт в Redis, попытка
// засчитывается до проверки пароля (take) и сбрасывается верным паролем. Те же правила —
// у ввода текущего пароля при его смене в профиле, но со своим счётчиком по id
// пользователя (см. ChangePassword).
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

// wait — сколько ещё держится блокировка (ключ уже насчитал maxFailures).
func (l lockout) wait(ctx context.Context, id string) (bool, time.Duration, error) {
	ttl, err := l.rdb.TTL(ctx, l.key(id)).Result()
	if err != nil {
		return false, 0, fmt.Errorf("lockout ttl: %w", err)
	}
	switch ttl {
	case -2:
		// Ключ истёк или пропал между INCR и TTL — блокировки уже нет.
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

// take засчитывает попытку ДО проверки пароля: иначе параллельные запросы разом проходят
// проверку блокировки, и сверх maxFailures проверяется сколько угодно паролей. Попытка сверх
// maxFailures отклоняется (locked) со сроком ожидания. После take неверный пароль отмечается
// takeFailed(n) — сама попытка уже учтена, верный — reset.
func (l lockout) take(ctx context.Context, id string) (n int64, locked bool, wait time.Duration, err error) {
	pipe := l.rdb.TxPipeline()
	incr := pipe.Incr(ctx, l.key(id))
	pipe.ExpireNX(ctx, l.key(id), lockoutWindow)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, false, 0, fmt.Errorf("lockout incr: %w", err)
	}
	n = incr.Val()
	if n <= maxFailures {
		return n, false, 0, nil
	}
	locked, wait, err = l.wait(ctx, id)
	if err == nil && !locked {
		// Ключ истёк между INCR и TTL: эта попытка ушла в старый счётчик — засчитываем заново.
		return l.take(ctx, id)
	}
	return n, locked, wait, err
}

// releaseScript — DECR только существующего ключа: у пропавшего DECR создал бы «-1» без срока.
var releaseScript = redis.NewScript(`if redis.call("EXISTS", KEYS[1]) == 1 then return redis.call("DECR", KEYS[1]) end return 0`)

// release возвращает попытку, взятую take, когда пароль так и не проверен (сбой базы) или
// оказался верным, но войти нельзя (пользователь деактивирован): это не неудача.
// Записи после вердикта — без отмены контекста: разрыв соединения клиентом не должен
// ни сохранять попытку, ни терять неудачу.
func (l lockout) release(ctx context.Context, id string) error {
	if err := releaseScript.Run(context.WithoutCancel(ctx), l.rdb, []string{l.key(id)}).Err(); err != nil {
		return fmt.Errorf("lockout release: %w", err)
	}
	return nil
}

// takeFailed — неверный пароль после take: с maxFailures-й неудачи блокировка держится ровно lockoutWindow.
func (l lockout) takeFailed(ctx context.Context, id string, n int64) error {
	if n < maxFailures {
		return nil
	}
	if err := l.rdb.Expire(context.WithoutCancel(ctx), l.key(id), lockoutWindow).Err(); err != nil {
		return fmt.Errorf("lockout expire: %w", err)
	}
	return nil
}

func (l lockout) reset(ctx context.Context, id string) error {
	return l.rdb.Del(context.WithoutCancel(ctx), l.key(id)).Err()
}
