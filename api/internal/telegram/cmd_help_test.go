package telegram_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/telegram"
)

// helpBot — личные команды, /help и заглушка групповой команды /take (меню показывает только
// зарегистрированные команды).
func newHelpBot(t *testing.T) (*cmdBot, *telegram.Router) {
	t.Helper()
	var router *telegram.Router
	b := newCmdBot(t, func(rt *telegram.Router) {
		rt.Command("take", func(ctx context.Context, r *telegram.Request) error { return r.Reply(ctx, "взята") })
		telegram.RegisterHelp(rt)
		router = rt
	})
	return b, router
}

// §8.5: /help — список команд, доступных в этом типе чата.
func TestHelpCommand(t *testing.T) {
	b, _ := newHelpBot(t)

	require.Equal(t, []string{strings.Join([]string{
		"Команды в личном чате:",
		"/start [код] — Привязать аккаунт по коду из профиля",
		"/unlink — Отвязать аккаунт",
		"/take <ключ> — Взять задачу в работу",
		"/timer <ключ> — Запустить таймер",
		"/stop — Остановить таймер",
		"/my — Мои открытые задачи",
		"/help — Список команд",
		"Ключ задачи пишется вместе с ключом проекта: TM-42.",
	}, "\n")}, b.say(t, b.member, "/help"))

	require.Equal(t, []string{strings.Join([]string{
		"Команды в группе:",
		"/take <ключ> — Взять задачу в работу",
		"/help — Список команд",
		"Если к группе подключён один проект, ключ проекта можно не писать: /take 42.",
	}, "\n")}, b.sayIn(t, b.member, -100700, models.ChatTypeSupergroup, "/help@TaskBot"))
}

// §8.5: неизвестная команда в личном чате — «Неизвестная команда. /help», в группе — молчание
// (даже от непривязанного пользователя).
func TestUnknownCommand(t *testing.T) {
	b, _ := newHelpBot(t)
	require.Equal(t, []string{"Неизвестная команда. /help"}, b.say(t, b.member, "/foo TM-1"))
	require.Empty(t, b.sayIn(t, b.member, -100700, models.ChatTypeSupergroup, "/foo"))

	stranger := newUser(t, b.g, "Без привязки")
	b.tg[stranger.ID] = tgID()
	require.Empty(t, b.sayIn(t, stranger, -100700, models.ChatTypeGroup, "/foo"))
	require.Equal(t, []string{"Сначала привяжите аккаунт: профиль → Привязать Telegram"}, b.say(t, stranger, "/foo"),
		"в личном чате непривязанный получает ответ §8.1")
}

// §8.5: меню бота регистрируется через setMyCommands отдельно для личных чатов и групп.
func TestSetMyCommands(t *testing.T) {
	b, rt := newHelpBot(t)
	require.NoError(t, telegram.SetMyCommands(context.Background(), b.api, rt))

	calls := b.env.tg.Calls("setMyCommands")
	require.Len(t, calls, 2)
	byScope := map[string]string{}
	for _, c := range calls {
		byScope[c.Params["scope"]] = c.Params["commands"]
	}
	private := byScope[`{"type":"all_private_chats"}`]
	group := byScope[`{"type":"all_group_chats"}`]
	for _, name := range []string{"start", "unlink", "take", "timer", "stop", "my", "help"} {
		require.Contains(t, private, `"command":"`+name+`"`)
	}
	require.Contains(t, private, `"description":"Запустить таймер"`)
	require.Contains(t, group, `"command":"take"`)
	require.Contains(t, group, `"command":"help"`)
	for _, name := range []string{"start", "unlink", "timer", "stop", "my"} {
		require.NotContains(t, group, `"command":"`+name+`"`, "личные команды не в меню группы")
	}

	// Ошибка Telegram возвращается вызывающему (воркер пишет её в лог).
	b.env.tg.Respond("setMyCommands", `{"ok":false,"error_code":400,"description":"Bad Request: bad commands"}`)
	require.Error(t, telegram.SetMyCommands(context.Background(), b.api, rt))
}

// §8.5, §3.2: команда, меняющая задачу архивного проекта, — «Проект TM в архиве, изменения
// недоступны».
func TestArchivedProjectReply(t *testing.T) {
	b, _ := newHelpBot(t)
	tk := b.working(t, "Экспорт")
	require.NoError(t, b.g.Exec(`UPDATE projects SET is_archived = true WHERE id = ?`, b.pr.ID).Error)

	require.Equal(t, []string{"Проект " + b.pr.Key + " в архиве, изменения недоступны"}, b.say(t, b.member, "/timer "+tk.Key))
	var timers int
	require.NoError(t, b.g.Raw(`SELECT count(*) FROM active_timers WHERE user_id = ?`, b.member.ID).Scan(&timers).Error)
	require.Zero(t, timers)
}
