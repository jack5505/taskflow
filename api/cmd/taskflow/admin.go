package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/migrations"
)

var errAdminUsage = errors.New("использование: taskflow admin create --email <email> [--name <имя>]")

// isUsageError — неверные аргументы или -h: main печатает их в stderr и выходит с кодом 2.
func isUsageError(err error) bool {
	return errors.Is(err, errAdminUsage) || errors.Is(err, flag.ErrHelp)
}

// runAdmin — служебные команды (ТЗ §7.1): `taskflow admin create --email <email> [--name <имя>]`
// создаёт администратора системы и выдаёт ссылку установки пароля: с SMTP — письмом через воркер,
// без SMTP — в консоль. Запуск на стенде: docker compose exec api /app/taskflow admin create --email <email>.
func runAdmin(ctx context.Context, cfg *config.Config, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "create" {
		return errAdminUsage
	}
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	email := fs.String("email", "", "email администратора (логин)")
	name := fs.String("name", "", "отображаемое имя, по умолчанию — часть email до @")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *email == "" || fs.NArg() > 0 {
		return errAdminUsage
	}

	// Та же проверка SMTP_*, что у воркера: с неверным SMTP_FROM письмо не ушло бы, а ссылка не напечаталась.
	smtp, err := mail.NewSMTP(cfg)
	if err != nil {
		return err
	}

	g, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(g) }()
	// Команду можно запустить и до первого старта api (docker compose run): схема должна быть.
	migrateCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := db.Migrate(migrateCtx, cfg.DatabaseURL, migrations.FS); err != nil {
		return err
	}

	svc := auth.NewService(cfg, g, nil)
	if smtp != nil {
		mq := queue.NewClient(cfg.RedisAddr)
		defer func() { _ = mq.Close() }()
		svc.WithMailer(mail.NewQueue(mq))
	}
	return adminCreate(ctx, svc, *email, *name, smtp != nil, out)
}

// adminCreate — сама команда, без разбора аргументов и подключений (для тестов).
func adminCreate(ctx context.Context, svc *auth.Service, email, name string, smtpConfigured bool, out io.Writer) error {
	ctx = audit.WithSource(ctx, audit.SourceCLI)
	u, res, err := svc.EnsureSystemAdmin(ctx, email, name)
	if err != nil {
		return fmt.Errorf("admin create: %w", err)
	}
	var msg strings.Builder
	switch res {
	case auth.AdminCreated:
		fmt.Fprintf(&msg, "Администратор %s создан.\n", u.Email)
	case auth.AdminPromoted:
		fmt.Fprintf(&msg, "Пользователь %s уже был — назначен администратором системы.\n", u.Email)
	case auth.AdminExisted:
		fmt.Fprintf(&msg, "Администратор %s уже есть, новый не создан.\n", u.Email)
	}
	if res != auth.AdminCreated && name != "" && name != u.DisplayName {
		fmt.Fprintf(&msg, "Имя не изменено, осталось «%s».\n", u.DisplayName)
	}

	switch {
	case auth.HasPassword(u):
		msg.WriteString("Пароль уже задан, ссылка не выдаётся. Если пароль забыт — восстановление на странице входа.\n")
	case smtpConfigured:
		if err := svc.SendPasswordSetLink(ctx, u); err != nil {
			return fmt.Errorf("admin create: %w", err)
		}
		msg.WriteString("Письмо со ссылкой для установки пароля поставлено в очередь, его отправит воркер.\n")
	default:
		link, err := svc.PasswordSetLink(ctx, u)
		if err != nil {
			return fmt.Errorf("admin create: %w", err)
		}
		fmt.Fprintf(&msg, "SMTP не настроен. Ссылка для установки пароля (действует 24 часа, одноразовая):\n%s\n", link)
	}
	_, err = io.WriteString(out, msg.String())
	return err
}
