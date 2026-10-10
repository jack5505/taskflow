// Package queue — очередь заданий asynq на Redis (ТЗ §10.2) и проверка Redis для /health.
package queue

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// Имена очередей: уведомления и сообщения бота важнее фоновой уборки.
const (
	QueueDefault = "default"
	QueueLow     = "low"
)

// Opts — параметры подключения asynq к Redis.
func Opts(redisAddr string) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: redisAddr, DialTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
}

// NewClient — клиент для постановки заданий (используется релеем outbox и планировщиком).
func NewClient(redisAddr string) *asynq.Client {
	return asynq.NewClient(Opts(redisAddr))
}

// NewRedis — прямой клиент go-redis для /health, лимитов и идемпотентности (ТЗ §8.8).
func NewRedis(redisAddr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: redisAddr, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second})
}

// Ping проверяет Redis в пределах ctx.
func Ping(ctx context.Context, rdb *redis.Client) error {
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}

// NewServer — сервер воркера: две очереди, повторы с экспоненциальной задержкой asynq по умолчанию.
func NewServer(redisAddr string, concurrency int) *asynq.Server {
	return asynq.NewServer(Opts(redisAddr), asynq.Config{
		Concurrency: concurrency,
		Queues:      map[string]int{QueueDefault: 6, QueueLow: 1},
		Logger:      slogAdapter{},
		ErrorHandler: asynq.ErrorHandlerFunc(func(_ context.Context, t *asynq.Task, err error) {
			slog.Error("asynq task failed", "type", t.Type(), "err", err)
		}),
	})
}

// slogAdapter направляет логи asynq в slog.
type slogAdapter struct{}

func (slogAdapter) Debug(args ...any) { slog.Debug(fmt.Sprint(args...)) }
func (slogAdapter) Info(args ...any)  { slog.Info(fmt.Sprint(args...)) }
func (slogAdapter) Warn(args ...any)  { slog.Warn(fmt.Sprint(args...)) }
func (slogAdapter) Error(args ...any) { slog.Error(fmt.Sprint(args...)) }
func (slogAdapter) Fatal(args ...any) { slog.Error(fmt.Sprint(args...)) }
