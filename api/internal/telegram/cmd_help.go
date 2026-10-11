package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// RegisterHelp подключает /help и ответ на неизвестные команды (§8.5). Регистрируется вместе
// с остальными командами: /help и меню бота показывают только зарегистрированные команды.
func RegisterHelp(rt *Router) {
	rt.Command("help", helpCommand(rt))
	rt.Command(UnknownCommand, unknownCommand)
}

// menuCommand — команда из таблицы §8.5 в меню бота и в /help.
type menuCommand struct {
	name, args string
	private    bool // работает в личном чате
	group      bool // работает в группе
}

// menu — команды §8.5 в порядке таблицы. Описания — в texts.go (cmd_menu_<имя>).
var menu = []menuCommand{
	{name: "start", args: "[код]", private: true},
	{name: "unlink", private: true},
	{name: "bind", args: "<код>", group: true},
	{name: "unbind", args: "[ключ проекта]", group: true},
	{name: "take", args: "<ключ>", private: true, group: true},
	{name: "drop", args: "<ключ>", private: true, group: true},
	{name: "review", args: "<ключ>", private: true, group: true},
	{name: "done", args: "<ключ>", private: true, group: true},
	{name: "log", args: "<ключ> <время> [комментарий]", private: true, group: true},
	{name: "timer", args: "<ключ>", private: true},
	{name: "stop", private: true},
	{name: "my", private: true},
	{name: "tasks", args: "[ключ проекта]", group: true},
	{name: "sprint", args: "[ключ проекта]", group: true},
	{name: "task", args: "<ключ>", private: true, group: true},
	{name: "new", args: "[ключ проекта] <название>", private: true, group: true},
	{name: "comment", args: "<ключ> <текст>", private: true, group: true},
	{name: "help", private: true, group: true},
}

// helpLine — строка /help.
type helpLine struct{ Name, Args, Description string }

// menuFor — команды для личного чата или группы, у которых есть обработчик.
func (rt *Router) menuFor(private bool) []helpLine {
	var out []helpLine
	for _, c := range menu {
		if (private && !c.private) || (!private && !c.group) || rt.commands[c.name] == nil {
			continue
		}
		out = append(out, helpLine{Name: c.name, Args: c.args, Description: Text("cmd_menu_"+c.name, nil)})
	}
	return out
}

// helpCommand — /help: команды, доступные в этом типе чата (§8.5). Работает и без привязки
// (§8.1): тогда после списка — подсказка, как привязать аккаунт, а в группе ответ удаляется
// через 30 секунд, как и единый ответ непривязанному: посторонние не засоряют чат и лимит группы.
func helpCommand(rt *Router) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		name := "cmd_help_group"
		if r.Private() {
			name = "cmd_help_private"
		}
		data := struct {
			Commands []helpLine
			Keyed    bool // есть команды с ключом задачи — подсказка про формат ключа
			Unlinked bool // аккаунт не привязан — подсказка про привязку
		}{Commands: rt.menuFor(r.Private()), Unlinked: r.User == nil}
		for _, c := range data.Commands {
			data.Keyed = data.Keyed || strings.HasPrefix(c.Args, "<ключ>")
		}
		if data.Unlinked {
			return r.ReplyTemp(ctx, Text(name, data))
		}
		return r.Reply(ctx, Text(name, data))
	}
}

// unknownCommand — §8.5: в личном чате «Неизвестная команда. /help». В группе сюда не доходит:
// неизвестные команды там игнорируются молча (Router.route).
func unknownCommand(ctx context.Context, r *Request) error {
	return r.Reply(ctx, Text("cmd_unknown", nil))
}

// CommandsAPI — метод Bot API для меню команд; *bot.Bot его реализует.
type CommandsAPI interface {
	SetMyCommands(ctx context.Context, params *bot.SetMyCommandsParams) (bool, error)
}

// Регистрация меню при старте воркера: предел одной попытки и число попыток. Telegram может
// быть недоступен в момент старта — повторяем с паузой 1, 2, 4, 8 секунд.
const (
	setCommandsTimeout  = 30 * time.Second
	setCommandsAttempts = 5
)

// setCommandsPause — пауза перед повтором (в тесте короче).
var setCommandsPause = backoff

// SetMyCommands регистрирует меню бота отдельно для личных чатов и для групп (§8.5). Вызывается
// при старте воркера после регистрации команд; сбой сети или Telegram повторяет до setCommandsAttempts раз,
// отказ Telegram (400, 403) не повторяет.
// Ошибка после всех попыток — только в лог: бот работает и без меню.
func SetMyCommands(ctx context.Context, api CommandsAPI, rt *Router) error {
	var err error
	for attempt := 1; ; attempt++ {
		err = setMyCommands(ctx, api, rt)
		if err == nil || permanent(err) || attempt == setCommandsAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(setCommandsPause(attempt)):
		}
	}
}

func setMyCommands(ctx context.Context, api CommandsAPI, rt *Router) error {
	ctx, cancel := context.WithTimeout(ctx, setCommandsTimeout)
	defer cancel()
	scopes := []struct {
		private bool
		scope   models.BotCommandScope
	}{
		{true, &models.BotCommandScopeAllPrivateChats{}},
		{false, &models.BotCommandScopeAllGroupChats{}},
	}
	for _, s := range scopes {
		cmds := []models.BotCommand{} // пустой список, а не null: его Telegram принимает
		for _, l := range rt.menuFor(s.private) {
			cmds = append(cmds, models.BotCommand{Command: l.Name, Description: l.Description})
		}
		if _, err := api.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: cmds, Scope: s.scope}); err != nil {
			return fmt.Errorf("telegram: setMyCommands: %w", err)
		}
	}
	slog.Info("telegram: commands menu set")
	return nil
}
