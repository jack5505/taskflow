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
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/queue"
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
		err = runAdmin(ctx, cfg, os.Args[2:])
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

	authSvc := auth.NewService(cfg, g, rdb).WithQueue(mq)
	authHandlers := auth.NewHandlers(authSvc, auth.SecureCookies(cfg.BaseURL))
	return httpserver.New(cfg, g, rdb).Mount(authHandlers.Register).Run(ctx)
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

// runAdmin — заглушка до реализации CLI (issue #11).
func runAdmin(_ context.Context, _ *config.Config, args []string) error {
	return fmt.Errorf("admin: not implemented yet (args: %v)", args)
}
