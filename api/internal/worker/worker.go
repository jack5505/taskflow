// Package worker — режим `taskflow worker` (ТЗ §10.1): релей outbox и сервер asynq.
// Здесь же — планировщики: снимки спринтов, сводки в группы и напоминания о сроках.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/outbox"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/sprint"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
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
	notification.Register(d, g)
	tasks := task.NewService(g, project.NewService(g))
	stopTimers := func(ctx context.Context) (int64, error) { return tasks.StopStaleTimers(ctx) }
	// Архивация и удаление проекта останавливают его таймеры (§3.2) на момент события.
	for _, typ := range []string{events.ProjectArchived, events.ProjectDeleted} {
		d.On(typ, "timers", func(ctx context.Context, env events.Envelope) error {
			var p struct {
				ProjectID uuid.UUID `json:"project_id"`
				At        time.Time `json:"at"`
			}
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return fmt.Errorf("worker: %s payload: %w", env.Type, err)
			}
			if p.At.IsZero() {
				p.At = time.Now() // событие до появления поля at
			}
			_, err := tasks.StopProjectTimers(ctx, p.ProjectID, p.At)
			return err
		})
	}

	smtp, err := mail.NewSMTP(cfg)
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	authSvc := auth.NewService(cfg, g, rdb).WithMailer(mail.NewQueue(client))
	mux := d.Mux()
	mux.Handle(mail.TaskType, mail.Handler(smtp))
	mux.Handle(auth.TaskPasswordReset, auth.PasswordResetHandler(authSvc))
	mux.Handle(auth.TaskPasswordInvite, auth.PasswordInviteHandler(authSvc))
	accounts := telegram.NewAccounts(g, cfg.TelegramBotUsername)
	var digest *telegram.Digest // сводки в группы и личные напоминания о сроке (§8.6, §8.7)
	if cfg.TelegramEnabled() {
		b, err := telegram.NewBot(cfg)
		if err != nil {
			return fmt.Errorf("worker: %w", err)
		}
		rt := telegram.NewRouter(cfg.TelegramBotUsername)
		telegram.RegisterAccount(rt, accounts, rdb)
		telegram.RegisterGroups(rt, telegram.NewGroups(g, cfg.TelegramBotUsername), rdb)
		telegram.RegisterPersonal(rt, d, g, tasks, rdb, client, cfg.BaseURL)
		telegram.Mount(mux, b, rt, rdb, client)
		digest = telegram.NewDigest(g, client, cfg.BaseURL)
	}

	srv := queue.NewServer(cfg.RedisAddr, 10)
	if err := srv.Start(mux); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		outbox.NewRelay(g, client).Run(ctx)
	}()
	go func() {
		defer wg.Done()
		runDaily(ctx, "refresh tokens cleanup", authSvc.CleanupExpired)
	}()
	go func() {
		defer wg.Done()
		runDaily(ctx, "notifications cleanup", func(ctx context.Context) (int64, error) {
			return notification.Cleanup(ctx, g, time.Now())
		})
	}()
	wg.Go(func() { sprint.RunSnapshots(ctx, g) }) // снимки спринтов в 00:00 по поясу проекта (§3.8)
	wg.Go(func() { runDaily(ctx, "one-time codes cleanup", accounts.CleanupCodes) })
	go func() {
		defer wg.Done()
		runEvery(ctx, "timers stop", time.Minute, stopTimers)
	}()
	// Напоминания о сроке (§8.7, §7.7): колокольчик всегда, личное сообщение — если бот включён.
	var sendDue func(context.Context, notification.Due) error
	if digest != nil {
		sendDue = digest.SendDue
		wg.Go(func() {
			runEvery(ctx, "daily summaries", time.Minute, func(ctx context.Context) (int64, error) { return digest.Summaries(ctx, time.Now()) })
		})
		wg.Go(func() {
			runDaily(ctx, "daily summaries cleanup", func(ctx context.Context) (int64, error) { return digest.CleanupSummaries(ctx, time.Now()) })
		})
	}
	wg.Go(func() {
		runEvery(ctx, "due reminders", notification.DueEvery, func(ctx context.Context) (int64, error) {
			return notification.DueReminders(ctx, g, time.Now(), sendDue)
		})
	})
	wg.Go(func() {
		runDaily(ctx, "due reminders cleanup", func(ctx context.Context) (int64, error) { return notification.CleanupDue(ctx, g, time.Now()) })
	})
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

// runDaily выполняет уборку при старте и затем раз в сутки, пока ctx жив.
func runDaily(ctx context.Context, name string, job func(context.Context) (int64, error)) {
	runEvery(ctx, name, 24*time.Hour, job)
}

// runEvery выполняет job при старте и затем каждые interval, пока ctx жив. Результат — число
// обработанных строк (удалённых, остановленных) для лога.
func runEvery(ctx context.Context, name string, interval time.Duration, job func(context.Context) (int64, error)) {
	run := func() {
		jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if n, err := job(jobCtx); err != nil {
			slog.Error(name, "err", err)
		} else if n > 0 {
			slog.Info(name, "rows", n)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
