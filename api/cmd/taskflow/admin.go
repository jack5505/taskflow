package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/queue"
)

var errAdminUsage = errors.New("использование: taskflow admin create --email <email> [--name <имя>]")

// runAdmin — служебные команды (ТЗ §7.1): `taskflow admin create --email <email> [--name <имя>]`
// создаёт администратора системы и отправляет ссылку установки пароля; без SMTP печатает её.
// Запуск на стенде: docker compose exec api /app/taskflow admin create --email <email>.
func runAdmin(ctx context.Context, cfg *config.Config, args []string) error {
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

	g, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(g) }()

	svc := auth.NewService(cfg, g, nil)
	smtpConfigured := cfg.SMTPHost != ""
	if smtpConfigured {
		mq := queue.NewClient(cfg.RedisAddr)
		defer func() { _ = mq.Close() }()
		svc.WithMailer(mail.NewQueue(mq))
	}
	return adminCreate(ctx, svc, *email, *name, smtpConfigured, os.Stdout)
}

// adminCreate — сама команда, без разбора аргументов и подключений (для тестов).
func adminCreate(ctx context.Context, svc *auth.Service, email, name string, smtpConfigured bool, out io.Writer) error {
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

	if smtpConfigured {
		if err := svc.SendPasswordSetLink(ctx, u); err != nil {
			return fmt.Errorf("admin create: %w", err)
		}
		msg.WriteString("Письмо со ссылкой для установки пароля поставлено в очередь, его отправит воркер.\n")
	} else {
		link, err := svc.PasswordSetLink(ctx, u)
		if err != nil {
			return fmt.Errorf("admin create: %w", err)
		}
		fmt.Fprintf(&msg, "SMTP не настроен. Ссылка для установки пароля (действует 24 часа, одноразовая):\n%s\n", link)
	}
	_, err = io.WriteString(out, msg.String())
	return err
}
