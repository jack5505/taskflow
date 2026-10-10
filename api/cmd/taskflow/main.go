// TaskFlow — система управления задачами и временем с Telegram-ботом.
//
// Один бинарник, три режима (ТЗ §10.1, §10.2):
//
//	taskflow api     — HTTP API, веб-интерфейс (embed) и приём webhook Telegram
//	taskflow worker  — очередь asynq: outbox, сообщения бота, письма, таймеры, сводки
//	taskflow admin   — служебные команды, например создание первого администратора (ТЗ §7.1)
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

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/httpserver"
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
	return httpserver.New(cfg, g).Run(ctx)
}

func openDB(ctx context.Context, cfg *config.Config) (*gorm.DB, error) {
	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.Open(openCtx, cfg.DatabaseURL)
}

// runWorker — заглушка до реализации очереди (issue #2).
func runWorker(ctx context.Context, _ *config.Config) error {
	slog.Info("worker: not implemented yet, waiting for shutdown")
	<-ctx.Done()
	return nil
}

// runAdmin — заглушка до реализации CLI (issue #11).
func runAdmin(_ context.Context, _ *config.Config, args []string) error {
	return fmt.Errorf("admin: not implemented yet (args: %v)", args)
}
