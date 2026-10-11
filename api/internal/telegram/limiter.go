package telegram

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// reserveScript — скользящее окно в отсортированном множестве: метки отправок за последнее окно.
// Есть место — добавляет метку и возвращает 0, иначе — сколько миллисекунд ждать, пока
// освободится самая старая.
var reserveScript = redis.NewScript(`
local key, now, window, limit = KEYS[1], tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)
if redis.call('ZCARD', key) < limit then
  redis.call('ZADD', key, now, ARGV[4])
  redis.call('PEXPIRE', key, window)
  return 0
end
local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
return tonumber(oldest[2]) + window - now
`)

// Limiter — лимит Telegram на сообщения в группу (§8.8): не больше GroupLimit за минуту.
// Окно скользящее: фиксированные минуты пропустили бы 40 сообщений за две секунды на стыке.
// Счётчик в Redis общий для всех процессов воркера.
type Limiter struct {
	rdb *redis.Client
	now func() time.Time
}

func NewLimiter(rdb *redis.Client) *Limiter { return &Limiter{rdb: rdb, now: time.Now} }

// Reserve занимает место для сообщения в чат chatID. 0 — можно отправлять сейчас,
// иначе — через сколько попробовать снова (место не занято).
func (l *Limiter) Reserve(ctx context.Context, chatID int64) (time.Duration, error) {
	key := "tg:rate:" + strconv.FormatInt(chatID, 10)
	ms, err := reserveScript.Run(ctx, l.rdb, []string{key},
		l.now().UnixMilli(), limitWindow.Milliseconds(), GroupLimit, uuid.NewString()).Int64()
	if err != nil {
		return 0, fmt.Errorf("telegram: rate limit: %w", err)
	}
	if ms <= 0 {
		return 0, nil
	}
	return time.Duration(ms) * time.Millisecond, nil
}
