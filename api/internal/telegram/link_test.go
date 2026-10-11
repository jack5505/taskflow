package telegram_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

// bot — роутер с привязкой аккаунта и одной командой /take, которой нужен привязанный пользователь.
type linkBot struct {
	*env
	g        *gorm.DB
	accounts *telegram.Accounts
	p        *telegram.Processor
	actor    *uuid.UUID
	takes    int
}

func newLinkBot(t *testing.T) *linkBot {
	e := newEnv(t)
	g := testutil.SharedDB(t)
	b := &linkBot{env: e, g: g, accounts: telegram.NewAccounts(g, "TaskBot")}
	rt := telegram.NewRouter("TaskBot")
	telegram.RegisterAccount(rt, b.accounts, e.rdb)
	rt.Command("take", func(ctx context.Context, r *telegram.Request) error {
		b.takes++
		b.actor, _ = audit.From(ctx)
		require.Equal(t, *b.actor, r.User.ID)
		return r.Reply(ctx, "взята")
	})
	rt.Callback("take", func(ctx context.Context, r *telegram.Request) error {
		b.takes++
		return r.Answer(ctx, "взята", false)
	})
	b.p = telegram.NewProcessor(e.api, rt, e.out)
	return b
}

func (b *linkBot) send(from int64, chatID int64, chatType models.ChatType, text string) {
	upd := msgUpdate(chatID, chatType, text)
	upd.Message.From.ID = from
	b.p.Handle(context.Background(), upd, time.Now())
}

func (b *linkBot) replies(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range b.messages(t) {
		out = append(out, m.Text)
	}
	return out
}

// Сценарий §8.1: код из профиля → /start <код> в личном чате → «Аккаунт привязан: …».
func TestStartLinksAccount(t *testing.T) {
	b := newLinkBot(t)
	u := newUser(t, b.g, "Иван Петров")
	tg := tgID()
	code, err := b.accounts.IssueLinkCode(context.Background(), u.ID)
	require.NoError(t, err)

	b.send(tg, tg, models.ChatTypePrivate, "/start "+code.Code)
	require.Equal(t, []string{"Аккаунт привязан: Иван Петров (" + u.Email + ")"}, b.replies(t))
	require.Equal(t, tg, *reload(t, b.g, u.ID).TelegramUserID)
	require.Equal(t, "ivan", *reload(t, b.g, u.ID).TelegramUsername)

	// Теперь команды работают от имени пользователя.
	b.send(tg, tg, models.ChatTypePrivate, "/take TM-1")
	require.Equal(t, 1, b.takes)
	require.Equal(t, u.ID, *b.actor)
	require.Equal(t, []string{"взята"}, b.replies(t))

	// /start без кода привязанному — кто он.
	b.send(tg, tg, models.ChatTypePrivate, "/start")
	require.Equal(t, []string{"Аккаунт привязан: Иван Петров (" + u.Email + ")"}, b.replies(t))
}

func TestStartAnswers(t *testing.T) {
	b := newLinkBot(t)
	tg := tgID()

	b.send(tg, tg, models.ChatTypePrivate, "/start")
	require.Equal(t, []string{telegram.Text("start", nil)}, b.replies(t), "без кода — приветствие и инструкция")

	b.send(tg, tg, models.ChatTypePrivate, "/start ZZZZ2222")
	require.Equal(t, []string{"Код не найден или устарел. Сгенерируйте новый в профиле"}, b.replies(t))

	// Telegram уже привязан к другому аккаунту.
	first, second := newUser(t, b.g, "Первый"), newUser(t, b.g, "Второй")
	c1, err := b.accounts.IssueLinkCode(context.Background(), first.ID)
	require.NoError(t, err)
	b.send(tg, tg, models.ChatTypePrivate, "/start "+c1.Code)
	b.messages(t)
	c2, err := b.accounts.IssueLinkCode(context.Background(), second.ID)
	require.NoError(t, err)
	b.send(tg, tg, models.ChatTypePrivate, "/start "+c2.Code)
	require.Equal(t, []string{"Этот Telegram уже привязан к другому аккаунту. Сначала отвяжите его командой /unlink"}, b.replies(t))

	// /start в группе — только в личном чате; ответ удаляется через 30 секунд.
	b.send(tg, -100, models.ChatTypeGroup, "/start "+c2.Code)
	msgs := b.messages(t)
	require.Len(t, msgs, 1)
	require.Equal(t, telegram.Text("private_only", nil), msgs[0].Text)
	require.Equal(t, 30, msgs[0].DeleteAfter)
}

// §8.1 п. 6: /unlink отвязывает; после этого бот отвечает только на /start и /help (TestHelpUnlinked), на остальное —
// «Сначала привяжите аккаунт».
func TestUnlinkCommand(t *testing.T) {
	b := newLinkBot(t)
	u := newUser(t, b.g, "Анна")
	tg := tgID()
	code, err := b.accounts.IssueLinkCode(context.Background(), u.ID)
	require.NoError(t, err)
	b.send(tg, tg, models.ChatTypePrivate, "/start "+code.Code)
	b.messages(t)

	b.send(tg, -100, models.ChatTypeGroup, "/unlink")
	require.Equal(t, []string{telegram.Text("private_only", nil)}, b.replies(t), "в группе не отвязываем")
	require.NotNil(t, reload(t, b.g, u.ID).TelegramUserID)

	b.send(tg, tg, models.ChatTypePrivate, "/unlink")
	require.Equal(t, []string{"Аккаунт отвязан"}, b.replies(t))
	require.Nil(t, reload(t, b.g, u.ID).TelegramUserID)

	b.send(tg, tg, models.ChatTypePrivate, "/take TM-1")
	b.send(tg, tg, models.ChatTypePrivate, "/unlink")
	notLinked := "Сначала привяжите аккаунт: профиль → Привязать Telegram"
	require.Equal(t, []string{notLinked, notLinked}, b.replies(t))
	require.Zero(t, b.takes-0, "команда непривязанного не выполняется")
	b.send(tg, tg, models.ChatTypePrivate, "/start")
	require.Equal(t, []string{telegram.Text("start", nil)}, b.replies(t), "/start работает и без привязки")
}

// §8.1: непривязанному в группе — ответ на сообщение, который бот удаляет через 30 секунд;
// на кнопку — всплывающее уведомление.
func TestNotLinkedInGroup(t *testing.T) {
	b := newLinkBot(t)
	tg := tgID()

	b.send(tg, -100, models.ChatTypeGroup, "/take TM-1")
	msgs := b.messages(t)
	require.Equal(t, []telegram.Message{{Op: telegram.OpSend, ChatID: -100, ReplyTo: 7,
		Text: "Сначала привяжите аккаунт: профиль → Привязать Telegram", DeleteAfter: 30}}, msgs)

	upd := callbackUpdate(-100, "take:1")
	upd.CallbackQuery.From.ID = tg
	b.p.Handle(context.Background(), upd, time.Now())
	calls := b.tg.Calls("answerCallbackQuery")
	require.Len(t, calls, 1)
	require.Equal(t, "Сначала привяжите аккаунт: профиль → Привязать Telegram", calls[0].Params["text"])
	require.Equal(t, "true", calls[0].Params["show_alert"])
	require.Zero(t, b.takes)
	require.Empty(t, b.messages(t))

	// Временный ответ доходит до Telegram и через 30 секунд удаляется.
	d := telegram.NewDeliverer(b.api, b.rdb, b.out)
	require.NoError(t, b.deliver(t, d, msgs[0]))
	del, in := b.pendingMessage(t)
	require.Equal(t, telegram.OpDelete, del.Op)
	require.InDelta(t, 30, in.Seconds(), 2)
}

// Перебор кодов: после 10 неверных кодов за час с одного Telegram /start <код> не проверяется,
// даже верный код.
func TestStartLimitsCodeGuessing(t *testing.T) {
	b := newLinkBot(t)
	u := newUser(t, b.g, "Цель")
	tg := tgID()
	for range 10 {
		b.send(tg, tg, models.ChatTypePrivate, "/start ZZZZ2222")
	}
	require.Len(t, b.replies(t), 10)
	ttl, err := b.rdb.TTL(context.Background(), "tg:link-fail:"+strconv.FormatInt(tg, 10)).Result()
	require.NoError(t, err)
	require.InDelta(t, time.Hour.Seconds(), ttl.Seconds(), 60)

	code, err := b.accounts.IssueLinkCode(context.Background(), u.ID)
	require.NoError(t, err)
	b.send(tg, tg, models.ChatTypePrivate, "/start "+code.Code)
	require.Equal(t, []string{"Слишком много неверных кодов. Попробуйте через час"}, b.replies(t))
	require.Nil(t, reload(t, b.g, u.ID).TelegramUserID)

	other := tgID()
	b.send(other, other, models.ChatTypePrivate, "/start "+code.Code)
	require.Equal(t, []string{"Аккаунт привязан: Цель (" + u.Email + ")"}, b.replies(t), "счётчик — на Telegram-аккаунт")
}
