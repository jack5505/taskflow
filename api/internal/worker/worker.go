// Package worker — режим `taskflow worker` (ТЗ §10.1): релей outbox и сервер asynq.
// Сюда же позже встанут планировщик сводок и напоминаний, таймеры и отправка писем.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/outbox"
	"github.com/jack5505/taskflow/api/internal/queue"
)

// Run запускает релей и сервер asynq и ждёт отмены контекста.
// Порядок остановки: сервер asynq дорабатывает текущие задания, релей — текущую пачку,
// и только потом вызывающий закрывает пул базы.
func Run(ctx context.Context, cfg *config.Config, g *gorm.DB) error {
	client := queue.NewClient(cfg.RedisAddr)
	defer func() { _ = client.Close() }()
	rdb := queue.NewRedis(cfg.RedisAddr)
	defer func() { _ = rdb.Close() }()

	// Сервер asynq без Redis не падает, а молча повторяет подключение. Проверяем сами:
	// пусть контейнер упадёт и перезапустится, чем будет выглядеть живым без очереди.
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := queue.Ping(pingCtx, rdb); err != nil {
		if ctx.Err() != nil {
			return nil // остановка во время проверки — не ошибка
		}
		return fmt.Errorf("worker: %w", err)
	}

	d := events.NewDispatcher(rdb)
	registerHandlers(d)

	srv := queue.NewServer(cfg.RedisAddr, 10)
	if err := srv.Start(d.Mux()); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		outbox.NewRelay(g, client).Run(ctx)
	}()
	slog.Info("worker: started", "redis", cfg.RedisAddr)

	<-ctx.Done()
	srv.Shutdown()
	wg.Wait()
	return nil
}

// registerHandlers подключает обработчики событий. Пока только лог: домены добавят свои
// (сообщения бота, уведомления, письма) в следующих задачах.
func registerHandlers(d *events.Dispatcher) {
	d.On("*", "log", func(_ context.Context, env events.Envelope) error {
		slog.Info("event", "type", env.Type, "event_id", env.EventID)
		return nil
	})
}
