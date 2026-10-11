package telegram_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

// pending — одно отложенное сообщение из очереди и через сколько оно уйдёт.
func (e *env) pendingMessage(t *testing.T) (telegram.Message, time.Duration) {
	t.Helper()
	tasks := e.take(t, telegram.TaskSend)
	require.Len(t, tasks, 1)
	require.Equal(t, 3, tasks[0].MaxRetry, "повторы asynq — только на сбой воркера, не Telegram")
	var m telegram.Message
	require.NoError(t, json.Unmarshal(tasks[0].Payload, &m))
	return m, time.Until(tasks[0].NextProcessAt)
}

func TestDeliverSendsWithReplyAndKeyboard(t *testing.T) {
	e := newEnv(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Взять в работу", CallbackData: "take:1"}}}}
	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: -100, ReplyTo: 9, Text: "<b>TM-1</b>", HTML: true, Keyboard: kb}))

	calls := e.tg.Calls("sendMessage")
	require.Len(t, calls, 1)
	p := calls[0].Params
	require.Equal(t, "-100", p["chat_id"])
	require.Equal(t, "<b>TM-1</b>", p["text"])
	require.Equal(t, "HTML", p["parse_mode"])
	require.Contains(t, p["reply_parameters"], `"message_id":9`)
	require.Contains(t, p["reply_markup"], `"callback_data":"take:1"`)
	require.Empty(t, e.queued(t, telegram.TaskSend), "без delete_after удаление не планируется")
}

// Временный ответ в группе удаляется через 30 секунд (§8.1, §8.4).
func TestDeliverSchedulesDeleteOfTempReply(t *testing.T) {
	e := newEnv(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: -200, Text: "Сначала привяжите", DeleteAfter: 30}))

	del, in := e.pendingMessage(t)
	require.Equal(t, telegram.OpDelete, del.Op)
	require.EqualValues(t, -200, del.ChatID)
	require.Equal(t, 101, del.MessageID, "id сообщения из ответа sendMessage")
	require.InDelta(t, 30, in.Seconds(), 2)

	require.NoError(t, e.deliver(t, d, del))
	calls := e.tg.Calls("deleteMessage")
	require.Len(t, calls, 1)
	require.Equal(t, "101", calls[0].Params["message_id"])
}

// Критерий #48: 429 от Telegram — повтор через retry_after.
func TestDeliverRetriesAfterRetryAfter(t *testing.T) {
	e := newEnv(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	e.tg.Respond("sendMessage", testutil.TooManyRequests(7))

	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: 42, Text: "привет"}))
	m, in := e.pendingMessage(t)
	require.Equal(t, 1, m.Attempt)
	require.Equal(t, "привет", m.Text)
	require.InDelta(t, 7, in.Seconds(), 1.5, "повтор через retry_after")

	require.NoError(t, e.deliver(t, d, m))
	require.Len(t, e.tg.Calls("sendMessage"), 2)
	require.Empty(t, e.queued(t, telegram.TaskSend), "вторая попытка удалась")
}

// Без retry_after (5xx, сеть) — пауза 1, 2, 4, 8 секунд; после 5 неудачных попыток сообщение
// пишется в лог ошибок и отбрасывается (§8.8).
func TestDeliverGivesUpAfterFiveAttempts(t *testing.T) {
	e := newEnv(t)
	logs := testutil.CaptureLogs(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	m := telegram.Message{Op: telegram.OpSend, ChatID: 42, Text: "привет"}
	for attempt, want := range []float64{1, 2, 4, 8} {
		e.tg.Respond("sendMessage", `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
		require.NoError(t, e.deliver(t, d, m))
		var in time.Duration
		m, in = e.pendingMessage(t)
		require.Equal(t, attempt+1, m.Attempt)
		require.InDelta(t, want, in.Seconds(), 0.9)
	}
	e.tg.Respond("sendMessage", `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
	require.NoError(t, e.deliver(t, d, m))
	require.Empty(t, e.queued(t, telegram.TaskSend), "пятая неудача — сообщение отброшено")
	require.Len(t, e.tg.Calls("sendMessage"), telegram.MaxAttempts)
	require.True(t, logs.Contains("message dropped after retries"), logs.String())
	require.True(t, logs.Contains(`"level":"ERROR"`))
}

// Отказ, который повтор не исправит (бота удалили из группы), — сразу в лог, без повторов.
func TestDeliverDropsPermanentErrors(t *testing.T) {
	e := newEnv(t)
	logs := testutil.CaptureLogs(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	e.tg.Respond("sendMessage", `{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked from the group chat"}`)
	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: -300, Text: "x"}))
	require.Empty(t, e.queued(t, telegram.TaskSend))
	require.True(t, logs.Contains("Telegram refused it"))

	require.NoError(t, e.deliver(t, d, telegram.Message{Op: "fly", ChatID: 1}))
	require.Empty(t, e.queued(t, telegram.TaskSend))
}

func TestDeliverEdit(t *testing.T) {
	e := newEnv(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Открыть", URL: "https://tasks.example/t/TM-1"}}}}
	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpEdit, ChatID: -1, MessageID: 5, Text: "Статус: В работе", Keyboard: kb}))
	calls := e.tg.Calls("editMessageText")
	require.Len(t, calls, 1)
	require.Equal(t, "5", calls[0].Params["message_id"])
	require.Contains(t, calls[0].Params["reply_markup"], "tasks.example")

	// Тот же текст — Telegram отвечает «not modified»: это не ошибка и не повод повторять.
	e.tg.Respond("editMessageText", `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`)
	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpEdit, ChatID: -1, MessageID: 5, Text: "Статус: В работе"}))
	require.Empty(t, e.queued(t, telegram.TaskSend))
}

func TestDeliverRejectsBrokenPayload(t *testing.T) {
	e := newEnv(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	err := d.ProcessTask(context.Background(), asynq.NewTask(telegram.TaskSend, []byte("{")))
	require.ErrorIs(t, err, asynq.SkipRetry)
}

// §8.8: не более 20 сообщений в минуту в одну группу. 21-е ждёт освобождения окна и не
// считается неудачной попыткой; личные чаты и другие группы лимит не задевает.
func TestDeliverGroupRateLimit(t *testing.T) {
	e := newEnv(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	now := time.Now()
	d.Limiter().SetClock(func() time.Time { return now })

	for i := range telegram.GroupLimit {
		require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: -500, Text: "m"}), i)
		now = now.Add(time.Second)
	}
	require.Len(t, e.tg.Calls("sendMessage"), telegram.GroupLimit)

	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: -500, Text: "21-е"}))
	require.Len(t, e.tg.Calls("sendMessage"), telegram.GroupLimit, "21-е в ту же минуту не отправлено")
	m, in := e.pendingMessage(t)
	require.Equal(t, "21-е", m.Text)
	require.Zero(t, m.Attempt, "ожидание лимита — не неудачная попытка")
	require.InDelta(t, 40, in.Seconds(), 1.5, "первое сообщение окна выйдет из него через 60-20 секунд")

	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: -501, Text: "другая группа"}))
	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: 77, Text: "личный чат"}))
	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpDelete, ChatID: -500, MessageID: 3}))
	require.Len(t, e.tg.Calls("sendMessage"), telegram.GroupLimit+2)
	require.Len(t, e.tg.Calls("deleteMessage"), 1, "удаление лимитом не ограничено")

	now = now.Add(41 * time.Second) // самое старое сообщение вышло из окна
	require.NoError(t, e.deliver(t, d, m))
	require.Len(t, e.tg.Calls("sendMessage"), telegram.GroupLimit+3)
}

// Лимит общий для всех процессов воркера: одновременные попытки не превышают 20.
func TestLimiterIsAtomic(t *testing.T) {
	e := newEnv(t)
	l := telegram.NewLimiter(e.rdb)
	results := make(chan time.Duration, 50)
	for range 50 {
		go func() {
			w, err := l.Reserve(context.Background(), -900)
			if err != nil {
				w = -1
			}
			results <- w
		}()
	}
	allowed := 0
	for range 50 {
		w := <-results
		require.GreaterOrEqual(t, w, time.Duration(0))
		if w == 0 {
			allowed++
		}
	}
	require.Equal(t, telegram.GroupLimit, allowed)
}

// Redis лимитера недоступен — ошибка задания: asynq повторит его, сообщение не потеряно.
func TestDeliverFailsTaskWhenLimiterIsDown(t *testing.T) {
	e := newEnv(t)
	broken := queue.NewRedis("127.0.0.1:1")
	t.Cleanup(func() { _ = broken.Close() })
	d := telegram.NewDeliverer(e.api, broken, e.out)
	require.Error(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: -1, Text: "x"}))
	require.Empty(t, e.tg.Calls("sendMessage"))
}
