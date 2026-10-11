package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

func msgUpdate(chatID int64, chatType models.ChatType, text string) *models.Update {
	return &models.Update{ID: 1, Message: &models.Message{ID: 7, Text: text,
		Chat: models.Chat{ID: chatID, Type: chatType}, From: &models.User{ID: 42, Username: "ivan"}}}
}

func callbackUpdate(chatID int64, data string) *models.Update {
	return &models.Update{ID: 2, CallbackQuery: &models.CallbackQuery{ID: "cb-1", Data: data, From: models.User{ID: 42},
		Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 9, Chat: models.Chat{ID: chatID, Type: models.ChatTypeSupergroup}}}}}
}

func TestParseCommand(t *testing.T) {
	rt := telegram.NewRouter("@TaskBot")
	for _, c := range []struct {
		text, name, args string
		ok               bool
	}{
		{"/start", "start", "", true},
		{"/start AB12CD34", "start", "AB12CD34", true},
		{"/Take@taskbot  TM-42 ", "take", "TM-42", true},
		{"/log\nTM-1 1h", "log", "TM-1 1h", true},
		{"/take@OtherBot TM-1", "", "", false},
		{"просто текст", "", "", false},
		{"/", "", "", false},
		{"/@TaskBot", "", "", false},
	} {
		name, args, ok := rt.ParseCommand(c.text)
		require.Equal(t, c.ok, ok, c.text)
		require.Equal(t, c.name, name, c.text)
		require.Equal(t, c.args, args, c.text)
	}
	_, _, ok := telegram.NewRouter("").ParseCommand("/take@TaskBot 1")
	require.False(t, ok, "без имени бота адресованные команды не принимаем")
}

// Команда вызывает обработчик с источником telegram; ответ в группе — reply на команду.
func TestProcessorRoutesCommand(t *testing.T) {
	e := newEnv(t)
	rt := telegram.NewRouter("TaskBot")
	var got *telegram.Request
	var source string
	rt.Command("take", func(ctx context.Context, r *telegram.Request) error {
		got = r
		_, source = audit.From(ctx)
		return r.Reply(ctx, "взята")
	})
	var order []string
	rt.Use(func(next telegram.HandlerFunc) telegram.HandlerFunc {
		return func(ctx context.Context, r *telegram.Request) error {
			order = append(order, "outer")
			return next(ctx, r)
		}
	})
	rt.Use(func(next telegram.HandlerFunc) telegram.HandlerFunc {
		return func(ctx context.Context, r *telegram.Request) error {
			order = append(order, "inner")
			return next(ctx, r)
		}
	})
	p := telegram.NewProcessor(e.api, rt, e.out)

	p.Handle(context.Background(), msgUpdate(-100, models.ChatTypeGroup, "/take TM-42"), time.Now())
	require.NotNil(t, got)
	require.Equal(t, "TM-42", got.Args)
	require.False(t, got.Private())
	require.False(t, got.IsCallback())
	require.Equal(t, audit.SourceBot, source)
	require.Equal(t, []string{"outer", "inner"}, order)
	msgs := e.messages(t)
	require.Equal(t, []telegram.Message{{Op: telegram.OpSend, ChatID: -100, ReplyTo: 7, Text: "взята"}}, msgs)

	// Не команда, чужой бот, команда без обработчика — молча пропускаются.
	got = nil
	for _, text := range []string{"привет", "/take@OtherBot 1", "/unknown"} {
		p.Handle(context.Background(), msgUpdate(-100, models.ChatTypeGroup, text), time.Now())
	}
	p.Handle(context.Background(), &models.Update{ID: 3}, time.Now())
	require.Nil(t, got)
	require.Empty(t, e.messages(t))

	// Обработчик неизвестных команд.
	rt.Command(telegram.UnknownCommand, func(ctx context.Context, r *telegram.Request) error {
		return r.Reply(ctx, "Неизвестная команда: "+r.Command)
	})
	p.Handle(context.Background(), msgUpdate(42, models.ChatTypePrivate, "/unknown"), time.Now())
	require.Equal(t, []telegram.Message{{Op: telegram.OpSend, ChatID: 42, Text: "Неизвестная команда: unknown"}}, e.messages(t),
		"в личном чате ответ без reply")
}

func TestProcessorCommandErrorAndTempReply(t *testing.T) {
	e := newEnv(t)
	logs := testutil.CaptureLogs(t)
	rt := telegram.NewRouter("TaskBot")
	rt.Command("fail", func(context.Context, *telegram.Request) error { return errors.New("база недоступна") })
	rt.Command("temp", func(ctx context.Context, r *telegram.Request) error { return r.Answer(ctx, "временно", true) })
	p := telegram.NewProcessor(e.api, rt, e.out)

	p.Handle(context.Background(), msgUpdate(42, models.ChatTypePrivate, "/fail"), time.Now())
	msgs := e.messages(t)
	require.Len(t, msgs, 1)
	require.Equal(t, telegram.Text("internal_error", nil), msgs[0].Text)
	require.True(t, logs.Contains("база недоступна"))

	p.Handle(context.Background(), msgUpdate(-100, models.ChatTypeGroup, "/temp"), time.Now())
	p.Handle(context.Background(), msgUpdate(42, models.ChatTypePrivate, "/temp"), time.Now())
	require.Equal(t, []telegram.Message{
		{Op: telegram.OpSend, ChatID: -100, ReplyTo: 7, Text: "временно", DeleteAfter: 30},
		{Op: telegram.OpSend, ChatID: 42, Text: "временно"},
	}, e.messages(t), "в группе ответ удаляется через 30 секунд, в личном чате остаётся")
}

// §8.8: нажатие кнопки подтверждается answerCallbackQuery с ответом обработчика, один раз.
func TestProcessorAnswersCallback(t *testing.T) {
	e := newEnv(t)
	rt := telegram.NewRouter("TaskBot")
	var got *telegram.Request
	rt.Callback("take", func(ctx context.Context, r *telegram.Request) error {
		got = r
		return r.Answer(ctx, "Задача TM-42 уже в работе", true)
	})
	p := telegram.NewProcessor(e.api, rt, e.out)

	p.Handle(context.Background(), callbackUpdate(-100, "take:0b5f"), time.Now())
	require.NotNil(t, got)
	require.True(t, got.IsCallback())
	require.Equal(t, "take", got.Action)
	require.Equal(t, "0b5f", got.Data)
	require.EqualValues(t, -100, got.Chat.ID)
	require.Equal(t, 9, got.MessageID)

	calls := e.tg.Calls("answerCallbackQuery")
	require.Len(t, calls, 1)
	require.Equal(t, "cb-1", calls[0].Params["callback_query_id"])
	require.Equal(t, "Задача TM-42 уже в работе", calls[0].Params["text"])
	require.Equal(t, "true", calls[0].Params["show_alert"])
	require.Empty(t, e.messages(t))

	// Неизвестная кнопка (старая версия бота) — пустое подтверждение, чтобы не крутился индикатор.
	p.Handle(context.Background(), callbackUpdate(-100, "legacy"), time.Now())
	calls = e.tg.Calls("answerCallbackQuery")
	require.Len(t, calls, 2)
	require.Empty(t, calls[1].Params["text"])

	// Ошибка обработчика — общий ответ всплывающим окном.
	rt.Callback("boom", func(context.Context, *telegram.Request) error { return errors.New("сбой") })
	p.Handle(context.Background(), callbackUpdate(-100, "boom:1"), time.Now())
	calls = e.tg.Calls("answerCallbackQuery")
	require.Equal(t, telegram.Text("internal_error", nil), calls[2].Params["text"])
}

// §8.8: подтверждение не позже чем через 3 секунды. Медленный обработчик — пустое подтверждение
// по таймеру, ответ уходит временным сообщением в чат.
func TestProcessorAcksSlowCallbackInTime(t *testing.T) {
	e := newEnv(t)
	rt := telegram.NewRouter("TaskBot")
	var finished time.Time
	rt.Callback("take", func(ctx context.Context, r *telegram.Request) error {
		time.Sleep(400 * time.Millisecond)
		finished = time.Now()
		return r.Answer(ctx, "Задача TM-42 взята", false)
	})
	p := telegram.NewProcessor(e.api, rt, e.out)
	p.SetClock(time.Now, 100*time.Millisecond)

	start := time.Now()
	p.Handle(context.Background(), callbackUpdate(-100, "take:1"), start)
	calls := e.tg.Calls("answerCallbackQuery")
	require.Len(t, calls, 1, "подтверждение ровно одно")
	require.Empty(t, calls[0].Params["text"])
	require.True(t, calls[0].At.Before(finished), "подтверждение ушло до конца обработчика")
	require.Less(t, calls[0].At.Sub(start), 350*time.Millisecond)
	require.Equal(t, []telegram.Message{{Op: telegram.OpSend, ChatID: -100, ReplyTo: 9, Text: "Задача TM-42 взята", DeleteAfter: 30}}, e.messages(t))

	// Обновление пролежало в очереди дольше срока — подтверждаем сразу.
	rt.Callback("fast", func(ctx context.Context, r *telegram.Request) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	start = time.Now()
	p.Handle(context.Background(), callbackUpdate(-100, "fast:1"), start.Add(-time.Second))
	calls = e.tg.Calls("answerCallbackQuery")
	require.Len(t, calls, 2)
	require.Less(t, calls[1].At.Sub(start), 90*time.Millisecond)
	require.Empty(t, e.messages(t), "пустой ответ обработчика — сообщения нет")
}

func TestProcessTaskPayload(t *testing.T) {
	e := newEnv(t)
	rt := telegram.NewRouter("TaskBot")
	var args string
	rt.Command("start", func(_ context.Context, r *telegram.Request) error { args = r.Args; return nil })
	p := telegram.NewProcessor(e.api, rt, e.out)

	raw, err := json.Marshal(map[string]any{"received_at": time.Now(), "update": json.RawMessage(update(1, "/start AB12CD34"))})
	require.NoError(t, err)
	require.NoError(t, p.ProcessTask(context.Background(), asynq.NewTask(telegram.TaskUpdate, raw)))
	require.Equal(t, "AB12CD34", args)

	for _, bad := range []string{"{", `{"update":"x"}`} {
		require.ErrorIs(t, p.ProcessTask(context.Background(), asynq.NewTask(telegram.TaskUpdate, []byte(bad))), asynq.SkipRetry, bad)
	}
}
