package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/jack5505/taskflow/api/internal/audit"
)

// publicCommands — команды, которые работают без привязки аккаунта (§8.1: после отвязки бот
// реагирует только на /start).
var publicCommands = map[string]bool{"start": true}

// Перебор кодов: после maxCodeFailures неверных кодов за linkFailWindow с одного Telegram-аккаунта
// /start <код> не проверяется до конца окна. Код — 40 бит, но живых кодов может быть много.
const (
	maxCodeFailures = 10
	linkFailWindow  = time.Hour
)

// RegisterAccount подключает привязку аккаунта (§8.1): middleware, определяющий пользователя
// по telegram_user_id, и команды /start и /unlink. rdb — счётчик неверных кодов.
func RegisterAccount(rt *Router, accounts *Accounts, rdb *redis.Client) {
	rt.Use(RequireLinked(accounts))
	rt.Command("start", startCommand(accounts, rdb))
	rt.Command("unlink", unlinkCommand(accounts))
}

// RequireLinked определяет пользователя по telegram_user_id отправителя и действует от его
// имени (audit.WithActor). Не привязан — на любую команду, кроме /start, и любую кнопку один и
// тот же ответ (§8.1): у кнопки — всплывающим окном, в группе — ответом, который удаляется
// через 30 секунд.
func RequireLinked(accounts *Accounts) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, r *Request) error {
			u, err := accounts.ByTelegram(ctx, r.From.ID, r.From.Username)
			if err != nil {
				return err
			}
			if u != nil {
				r.User = u
				ctx = audit.WithSource(audit.WithActor(ctx, u.ID), audit.SourceBot)
			} else if r.IsCallback() || !publicCommands[r.Command] {
				return r.Answer(ctx, Text("not_linked", nil), true)
			}
			return next(ctx, r)
		}
	}
}

// startCommand — /start [код] в личном чате: привязка по коду или приветствие с инструкцией.
func startCommand(accounts *Accounts, rdb *redis.Client) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		if !r.Private() {
			return r.Answer(ctx, Text("private_only", nil), true)
		}
		if r.Args == "" {
			if r.User != nil {
				return r.Reply(ctx, Text("linked", r.User))
			}
			return r.Reply(ctx, Text("start", nil))
		}
		failKey := "tg:link-fail:" + strconv.FormatInt(r.From.ID, 10)
		failures, err := rdb.Get(ctx, failKey).Int()
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("telegram: link failures: %w", err)
		}
		if failures >= maxCodeFailures {
			return r.Reply(ctx, Text("too_many_codes", nil))
		}
		u, err := accounts.Link(ctx, r.Args, r.From.ID, r.From.Username)
		switch {
		case errors.Is(err, ErrCodeInvalid):
			pipe := rdb.TxPipeline()
			pipe.Incr(ctx, failKey)
			pipe.ExpireNX(ctx, failKey, linkFailWindow)
			if _, err := pipe.Exec(ctx); err != nil {
				return fmt.Errorf("telegram: link failures: %w", err)
			}
			return r.Reply(ctx, Text("code_invalid", nil))
		case errors.Is(err, ErrLinkedToOther):
			return r.Reply(ctx, Text("linked_to_other", nil))
		case err != nil:
			return err
		}
		return r.Reply(ctx, Text("linked", u))
	}
}

// unlinkCommand — /unlink в личном чате (§8.1, шаг 6). Сюда доходит только привязанный.
func unlinkCommand(accounts *Accounts) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		if !r.Private() {
			return r.Answer(ctx, Text("private_only", nil), true)
		}
		if err := accounts.Unlink(ctx, r.User.ID); err != nil {
			return err
		}
		return r.Reply(ctx, Text("unlinked", nil))
	}
}
