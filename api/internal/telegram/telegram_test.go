package telegram_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/telegram"
)

// В режиме webhook API при старте сообщает Telegram адрес и секрет (§8.8).
func TestSetWebhook(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, telegram.SetWebhook(context.Background(), e.cfg))
	calls := e.tg.Calls("setWebhook")
	require.Len(t, calls, 1)
	require.Equal(t, "https://tasks.example/api/v1/telegram/webhook", calls[0].Params["url"])
	require.Equal(t, secret, calls[0].Params["secret_token"])
	require.Contains(t, calls[0].Params["allowed_updates"], "callback_query")

	e.tg.Respond("setWebhook", `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	require.ErrorContains(t, telegram.SetWebhook(context.Background(), e.cfg), "setWebhook")

	cfg := *e.cfg
	cfg.TelegramBotToken = ""
	require.Error(t, telegram.SetWebhook(context.Background(), &cfg), "без токена бота нет")
}

// Long polling (только разработка) снимает webhook и принимает обновления тем же Intake:
// повтор update_id так же отбрасывается.
func TestRunPolling(t *testing.T) {
	e := newEnv(t)
	e.tg.Respond("getUpdates", `{"ok":true,"result":[`+update(9001, "/start")+`,`+update(9002, "/help")+`]}`)
	e.tg.Delay("getUpdates", 20*time.Millisecond)
	require.NoError(t, e.rdb.Set(context.Background(), "tg:update:9002", "1", time.Hour).Err())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- telegram.RunPolling(ctx, e.cfg, telegram.NewIntake(e.rdb, e.jobs)) }()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, e.queued(t, telegram.TaskUpdate), 1)
	}, 10*time.Second, 50*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("RunPolling не завершился после отмены")
	}
	require.Len(t, e.tg.Calls("deleteWebhook"), 1)
	require.Contains(t, string(e.queued(t, telegram.TaskUpdate)[0].Payload), `"update_id":9001`)

	e.tg.Respond("deleteWebhook", `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	require.ErrorContains(t, telegram.RunPolling(context.Background(), e.cfg, nil), "deleteWebhook")
}

func TestTextFallsBackOnUnknownTemplate(t *testing.T) {
	require.Equal(t, telegram.Text("internal_error", nil), telegram.Text("нет_такого", nil))
	require.NotEmpty(t, telegram.Text("internal_error", nil))
}
