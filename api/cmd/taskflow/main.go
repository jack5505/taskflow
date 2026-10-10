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

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
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
		err = httpserver.Run(ctx, cfg)
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

// runWorker — заглушка до реализации очереди (issue «Воркер и outbox»).
func runWorker(ctx context.Context, _ *config.Config) error {
	slog.Info("worker: not implemented yet, waiting for shutdown")
	<-ctx.Done()
	return nil
}

// runAdmin — заглушка до реализации CLI (issue «Пользователи и первый администратор»).
func runAdmin(_ context.Context, _ *config.Config, args []string) error {
	return fmt.Errorf("admin: not implemented yet (args: %v)", args)
}
