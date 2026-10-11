// loadgen — генератор тестовых данных для нагрузочного теста (ТЗ §10.4): применяет миграции к
// пустой базе и заполняет её объёмом §10.3. Пароль всех пользователей load<N>@example.com — -password.
//
//	go run ./cmd/loadgen -dsn postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable
//
// Полный прогон со стендом и k6 — loadtest/run.sh. На рабочем стенде не запускать: генератор
// откажется писать в базу, где уже есть пользователи или проекты.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/loadgen"
	"github.com/jack5505/taskflow/api/migrations"
)

func main() {
	p := loadgen.Params{}
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "строка подключения PostgreSQL (по умолчанию DATABASE_URL)")
	password := flag.String("password", "loadtest-password", "пароль всех сгенерированных пользователей")
	today := flag.String("today", time.Now().Format(time.DateOnly), "последний день записей времени (ГГГГ-ММ-ДД)")
	flag.IntVar(&p.Users, "users", loadgen.DefaultUsers, "пользователей")
	flag.IntVar(&p.Projects, "projects", loadgen.DefaultProjects, "проектов")
	flag.IntVar(&p.Tasks, "tasks", loadgen.DefaultTasks, "задач")
	flag.IntVar(&p.Worklogs, "worklogs", loadgen.DefaultWorklogs, "записей времени")
	flag.Parse()

	if err := run(*dsn, *password, *today, p); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run(dsn, password, today string, p loadgen.Params) error {
	if dsn == "" {
		return fmt.Errorf("нужен -dsn или DATABASE_URL")
	}
	var err error
	if p.Today, err = time.Parse(time.DateOnly, today); err != nil {
		return fmt.Errorf("-today: %w", err)
	}
	if p.PasswordHash, err = auth.HashPassword(password); err != nil {
		return fmt.Errorf("-password: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := db.Migrate(ctx, dsn, migrations.FS); err != nil {
		return err
	}
	g, err := db.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(g) }()
	logf := func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
	return loadgen.Generate(ctx, g, p, logf)
}
