package testutil

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

var (
	redisOnce sync.Once
	redisAddr string
	redisErr  error
)

// SharedRedisAddr поднимает один контейнер redis:7-alpine на тестовый пакет и возвращает host:port.
func SharedRedisAddr(t *testing.T) string {
	t.Helper()
	skipIfShort(t)

	redisOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		c, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			redisErr = err
			return
		}
		uri, err := c.ConnectionString(ctx) // redis://host:port
		if err != nil {
			redisErr = err
			return
		}
		redisAddr = strings.TrimPrefix(uri, "redis://")
	})
	if redisErr != nil {
		t.Fatalf("shared redis: %v", redisErr)
	}
	return redisAddr
}
