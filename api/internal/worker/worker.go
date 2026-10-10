// Package worker — режим `taskflow worker` (ТЗ §10.1): релей outbox и сервер asynq.
// Сюда же позже встанут планировщик сводок и напоминаний, таймеры и отправка писем.
package worker

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/outbox"
	"github.com/jack5505/taskflow/api/internal/queue"
)

// Run запускает релей и сервер asynq и ждёт отмены контекста.
func Run(ctx context.Context, cfg *config.Config, g *gorm.DB) error {
	client := queue.NewClient(cfg.RedisAddr)
	defer func() { _ = client.Close() }()

	d := events.NewDispatcher()
	registerHandlers(d)

	relay := outbox.NewRelay(g, client)
	go relay.Run(ctx)

	srv := queue.NewServer(cfg.RedisAddr, 10)
	if err := srv.Start(d.Mux()); err != nil {
		return err
	}
	slog.Info("worker: started", "redis", cfg.RedisAddr)

	<-ctx.Done()
	srv.Shutdown()
	time.Sleep(100 * time.Millisecond) // дать релею дописать текущую пачку
	return nil
}

// registerHandlers подключает обработчики событий. Пока только лог: домены добавят свои
// (сообщения бота, уведомления, письма) в следующих задачах.
func registerHandlers(d *events.Dispatcher) {
	d.On("*", func(_ context.Context, env events.Envelope) error {
		slog.Info("event", "type", env.Type, "event_id", env.EventID)
		return nil
	})
}
