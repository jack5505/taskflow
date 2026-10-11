// TaskFlow — система управления задачами и временем с Telegram-ботом.
//
// Один бинарник, три режима (ТЗ §10.1, §10.2):
//
//	taskflow api     — HTTP API, веб-интерфейс (embed) и приём webhook Telegram
//	taskflow worker  — очередь asynq: outbox, сообщения бота, письма, таймеры, сводки
//	taskflow admin   — служебные команды, например создание первого администратора (ТЗ §7.1)
//
// Общие аннотации swag (ТЗ §10.3: Swagger 2.0 из кода, UI на /api/docs):
//
//	@title                      TaskFlow API
//	@version                    1.0
//	@description                Система управления задачами и временем с Telegram-ботом. Единый формат ошибок — ТЗ §9.3.
//	@BasePath                   /
//	@schemes                    https http
//	@securityDefinitions.apikey BearerAuth
//	@in                         header
//	@name                       Authorization
//	@description                JWT access-токен: "Bearer <token>" (ТЗ §9.1)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/app"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/worker"
	"github.com/jack5505/taskflow/api/migrations"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "api":
		err = runAPI(ctx, cfg)
	case "worker":
		err = runWorker(ctx, cfg)
	case "admin":
		// Команду читает человек в консоли: ошибки текстом в stderr, а не JSON-логом.
		if err := runAdmin(ctx, cfg, os.Args[2:], os.Stdout); err != nil {
			if !errors.Is(err, flag.ErrHelp) {
				fmt.Fprintln(os.Stderr, "ошибка:", err)
			}
			if isUsageError(err) {
				os.Exit(2)
			}
			os.Exit(1)
		}
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		logger.Error("exit", "mode", os.Args[1], "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: taskflow <api|worker|admin> [args]")
	fmt.Fprintln(os.Stderr, "       taskflow admin create --email <email> [--name <имя>]")
}

// runAPI: подключение к базе, миграции при старте (ТЗ §10.2), затем HTTP-сервер.
func runAPI(ctx context.Context, cfg *config.Config) error {
	g, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(g) }()

	migrateCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := db.Migrate(migrateCtx, cfg.DatabaseURL, migrations.FS); err != nil {
		return err
	}
	rdb := queue.NewRedis(cfg.RedisAddr)
	defer func() { _ = rdb.Close() }()

	mq := queue.NewClient(cfg.RedisAddr)
	defer func() { _ = mq.Close() }()

	startBot(ctx, cfg, telegram.NewIntake(rdb, mq))
	startMonitor(ctx, cfg, g, rdb)
	return app.NewServer(cfg, g, rdb, mq).Run(ctx)
}

// startBot: в режиме webhook сообщает Telegram адрес и секрет, в разработке — запускает long
// polling (§8.8). Ошибки не останавливают API: без бота веб работает.
func startBot(ctx context.Context, cfg *config.Config, in *telegram.Intake) {
	if !cfg.TelegramEnabled() {
		slog.Info("telegram: bot disabled, TELEGRAM_BOT_TOKEN is empty")
		return
	}
	if cfg.TelegramPolling {
		go func() {
			if err := telegram.RunPolling(ctx, cfg, in); err != nil && ctx.Err() == nil {
				slog.Error("telegram: polling", "err", err)
			}
		}()
		return
	}
	// setWebhook повторяется, пока не пройдёт: при первой выкладке HTTPS или Telegram могут быть
	// ещё недоступны, а без webhook бот молчит до перезапуска.
	go func() {
		for pause := 5 * time.Second; ; pause = min(2*pause, 10*time.Minute) {
			setCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := telegram.SetWebhook(setCtx, cfg)
			cancel()
			if err == nil || ctx.Err() != nil {
				return
			}
			slog.Error("telegram: setWebhook, retrying", "in", pause, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pause):
			}
		}
	}()
}

func openDB(ctx context.Context, cfg *config.Config) (*gorm.DB, error) {
	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.Open(openCtx, cfg.DatabaseURL)
}

// runWorker: релей outbox и сервер asynq (ТЗ §9.4, §10.1). Миграции применяет api.
func runWorker(ctx context.Context, cfg *config.Config) error {
	g, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(g) }()
	return worker.Run(ctx, cfg, g)
}

// startMonitor — оповещения администраторам §10.3 (база, Redis, воркер, диск) в личный чат бота;
// без бота — только записи уровня ERROR в логе.
func startMonitor(ctx context.Context, cfg *config.Config, g *gorm.DB, rdb *redis.Client) {
	var api telegram.API
	if cfg.TelegramEnabled() {
		b, err := telegram.NewBot(cfg)
		if err != nil {
			slog.Error("alert: bot", "err", err)
		} else {
			api = b
		}
	}
	go telegram.NewMonitor(telegram.NewAlerter(g, api), g, rdb, cfg.AttachmentsDir).Run(ctx)
}
