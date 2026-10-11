package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/redis/go-redis/v9"

	"github.com/jack5505/taskflow/api/internal/project"
)

// RegisterGroups подключает команды группы /bind и /unbind и служебные обновления чата (§8.2):
// удаление бота из группы, переход в супергруппу, переименование. rdb — счётчик неверных кодов.
//
// Бот работает в режиме конфиденциальности (privacy mode, настраивается у @BotFather): в группе
// он получает только команды, ответы на свои сообщения, нажатия кнопок и служебные сообщения,
// обычную переписку не видит и не хранит. Права администратора группы ему не нужны.
func RegisterGroups(rt *Router, groups *Groups, rdb *redis.Client) {
	rt.Command("bind", bindCommand(groups, rdb))
	rt.Command("unbind", unbindCommand(groups))
	rt.ChatUpdate(chatUpdate(groups))
}

// bindCommand — /bind <код> в группе (§8.2, шаги 2–4). Сюда доходит только привязанный.
func bindCommand(groups *Groups, rdb *redis.Client) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		if r.Private() {
			return r.Reply(ctx, Text("group_only", nil))
		}
		if r.Args == "" {
			return r.Reply(ctx, Text("bind_usage", nil))
		}
		// Перебор кодов ограничен так же, как у /start <код> (§8.1).
		failKey := "tg:bind-fail:" + strconv.FormatInt(r.From.ID, 10)
		failures, err := rdb.Get(ctx, failKey).Int()
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("telegram: bind failures: %w", err)
		}
		if failures >= maxCodeFailures {
			return r.Reply(ctx, Text("too_many_codes", nil))
		}
		p, err := groups.Bind(ctx, r.Args, r.Chat.ID, r.Chat.Title, r.User.ID)
		switch {
		case errors.Is(err, ErrCodeInvalid):
			pipe := rdb.TxPipeline()
			pipe.Incr(ctx, failKey)
			pipe.ExpireNX(ctx, failKey, linkFailWindow)
			if _, err := pipe.Exec(ctx); err != nil {
				return fmt.Errorf("telegram: bind failures: %w", err)
			}
			return r.Reply(ctx, Text("bind_code_invalid", nil))
		case err != nil:
			return r.Reply(ctx, groupErrorText(err, p))
		}
		return r.Reply(ctx, Text("group_bound", p))
	}
}

// unbindCommand — /unbind [ключ проекта] в группе (§8.2, шаг 5; §8.5): в группе с несколькими
// проектами ключ обязателен.
func unbindCommand(groups *Groups) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		if r.Private() {
			return r.Reply(ctx, Text("group_only", nil))
		}
		ps, err := groups.ChatProjects(ctx, r.Chat.ID)
		if err != nil {
			return err
		}
		var target *project.Project
		key := strings.ToUpper(strings.TrimSpace(r.Args))
		switch {
		case len(ps) == 0:
			return r.Reply(ctx, Text("group_no_projects", nil))
		case key == "" && len(ps) > 1:
			return r.Reply(ctx, Text("unbind_choose", ps))
		case key == "":
			target = &ps[0]
		default:
			for i := range ps {
				if ps[i].Key == key {
					target = &ps[i]
				}
			}
			if target == nil {
				return r.Reply(ctx, Text("unbind_not_here", key))
			}
		}
		p, err := groups.Unbind(ctx, target.ID, r.Chat.ID, r.User.ID)
		switch {
		case errors.Is(err, ErrGroupNotBound), errors.Is(err, project.ErrNotFound):
			return r.Reply(ctx, Text("unbind_not_here", target.Key)) // отключили одновременно
		case err != nil:
			return r.Reply(ctx, groupErrorText(err, p))
		}
		return r.Reply(ctx, Text("group_unbound", p))
	}
}

// groupErrorText — ответ бота на отказ в подключении или отключении (§8.2: «бот отвечает
// причиной»); неизвестная ошибка — общий ответ.
func groupErrorText(err error, p *project.Project) string {
	switch {
	case errors.Is(err, ErrNotManager):
		return Text("bind_not_manager", nil)
	case errors.Is(err, ErrProjectHasGroup) && p != nil:
		return Text("bind_has_group", p)
	case errors.Is(err, project.ErrArchived) && p != nil:
		return Text("project_archived", p)
	}
	slog.Error("telegram: group bind", "err", err)
	return Text("internal_error", nil)
}

// chatUpdate — служебные обновления группы (§8.2): бота удалили — привязка снимается у всех
// проектов группы и их менеджеры получают личное сообщение; группа стала супергруппой —
// проекты переходят на новый id чата; группу переименовали — новое название в настройках.
func chatUpdate(groups *Groups) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		if r.Private() {
			return nil // пользователь остановил бота в личном чате — это не группа
		}
		if cm := r.Update.MyChatMember; cm != nil {
			if st := cm.NewChatMember.Type; st != models.ChatMemberTypeLeft && st != models.ChatMemberTypeBanned {
				return nil
			}
			return botRemoved(ctx, groups, r)
		}
		msg := r.Update.Message
		switch {
		case msg.MigrateToChatID != 0:
			return migrate(ctx, groups, r.Chat.ID, msg.MigrateToChatID)
		case msg.MigrateFromChatID != 0:
			return migrate(ctx, groups, msg.MigrateFromChatID, r.Chat.ID)
		default:
			return groups.Rename(ctx, r.Chat.ID, msg.NewChatTitle)
		}
	}
}

// migrate — переход в супергруппу. Telegram присылает два служебных сообщения (в старый чат
// migrate_to_chat_id и в новый migrate_from_chat_id); второе уже ничего не меняет.
func migrate(ctx context.Context, groups *Groups, from, to int64) error {
	n, err := groups.Migrate(ctx, from, to)
	if err == nil && n > 0 {
		slog.Info("telegram: group migrated to supergroup", "from", from, "to", to, "projects", n)
	}
	return err
}

func botRemoved(ctx context.Context, groups *Groups, r *Request) error {
	ps, err := groups.BotRemoved(ctx, r.Chat.ID)
	if err != nil || len(ps) == 0 {
		return err
	}
	slog.Info("telegram: bot removed from group, projects unbound", "chat_id", r.Chat.ID, "projects", len(ps))
	managers, err := groups.ManagersToNotify(ctx, ps)
	if err != nil {
		return err
	}
	for tgID, list := range managers {
		text := Text("group_bot_removed", map[string]any{"Title": r.Chat.Title, "Projects": list})
		if err := r.out.Send(ctx, Message{Op: OpSend, ChatID: tgID, Text: text}, 0); err != nil {
			return err
		}
	}
	return nil
}
