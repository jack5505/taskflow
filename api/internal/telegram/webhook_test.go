package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/telegram"
)

func (e *env) router() http.Handler {
	in := telegram.NewIntake(e.rdb, e.jobs)
	return httpserver.New(&config.Config{Env: "test"}, nil, e.rdb).Mount(telegram.NewWebhook(secret, in).Register).Router()
}

func post(h http.Handler, secretHeader, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/telegram/webhook", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if secretHeader != "" {
		r.Header.Set(telegram.SecretHeader, secretHeader)
	}
	h.ServeHTTP(rec, r)
	return rec
}

func update(id int64, text string) string {
	return fmt.Sprintf(`{"update_id":%d,"message":{"message_id":7,"date":1,"chat":{"id":42,"type":"private"},"from":{"id":42,"first_name":"Иван","username":"ivan"},"text":%q}}`, id, text)
}

// §8.8: секрет проверяется на каждом запросе; без него обновление не принимается.
func TestWebhookChecksSecret(t *testing.T) {
	e := newEnv(t)
	h := e.router()
	for _, s := range []string{"", "wrong", secret + "x"} {
		rec := post(h, s, update(1, "/start"))
		require.Equal(t, http.StatusUnauthorized, rec.Code, s)
		require.Contains(t, rec.Body.String(), `"code":"UNAUTHORIZED"`)
	}
	require.Empty(t, e.queued(t, telegram.TaskUpdate), "без секрета ничего не ставится в очередь")
}

// §8.8: 200 сразу, обработка в очереди; повторная доставка того же update_id не ставит
// второго задания.
func TestWebhookAcceptsOnceAndAnswersImmediately(t *testing.T) {
	e := newEnv(t)
	h := e.router()

	rec := post(h, secret, update(1001, "/start"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"ok":true}`, rec.Body.String())
	rec = post(h, secret, update(1001, "/start"))
	require.Equal(t, http.StatusOK, rec.Code, "повтор — тоже 200, иначе Telegram будет слать его снова")
	require.Equal(t, http.StatusOK, post(h, secret, update(1002, "/start")).Code)

	tasks := e.queued(t, telegram.TaskUpdate)
	require.Len(t, tasks, 2, "1001 принят один раз, 1002 — отдельно")
	var job struct {
		ReceivedAt time.Time       `json:"received_at"`
		Update     json.RawMessage `json:"update"`
	}
	require.NoError(t, json.Unmarshal(tasks[0].Payload, &job))
	require.WithinDuration(t, time.Now(), job.ReceivedAt, time.Minute)
	require.Contains(t, string(job.Update), `"update_id":100`)
	require.Zero(t, tasks[0].MaxRetry, "update_id обрабатывается не более одного раза")

	ttl, err := e.rdb.TTL(context.Background(), "tg:update:1001").Result()
	require.NoError(t, err)
	require.InDelta(t, (24 * time.Hour).Seconds(), ttl.Seconds(), 60, "ProcessedUpdate хранится 24 часа")
}

func TestWebhookRejectsBadBody(t *testing.T) {
	e := newEnv(t)
	h := e.router()
	for _, body := range []string{"", "[]", "{", `{"message":{}}`, `{"update_id":"1"}`} {
		rec := post(h, secret, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Contains(t, rec.Body.String(), `"code":"INVALID_JSON"`, body)
	}
	rec := post(h, secret, `{"update_id":1,"x":"`+strings.Repeat("a", 1<<20)+`"}`)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Empty(t, e.queued(t, telegram.TaskUpdate))
}

// Очередь недоступна — обновление не принято (500), update_id не запомнен: повторная
// доставка от Telegram пройдёт.
func TestIntakeForgetsUpdateWhenQueueFails(t *testing.T) {
	e := newEnv(t)
	broken := queue.NewClient("127.0.0.1:1")
	t.Cleanup(func() { _ = broken.Close() })
	ctx := context.Background()

	_, err := telegram.NewIntake(e.rdb, broken).Accept(ctx, 77, []byte(update(77, "/start")))
	require.Error(t, err)
	n, err := e.rdb.Exists(ctx, "tg:update:77").Result()
	require.NoError(t, err)
	require.Zero(t, n)

	fresh, err := telegram.NewIntake(e.rdb, e.jobs).Accept(ctx, 77, []byte(update(77, "/start")))
	require.NoError(t, err)
	require.True(t, fresh)

	rec := post(httpserver.New(&config.Config{Env: "test"}, nil, e.rdb).
		Mount(telegram.NewWebhook(secret, telegram.NewIntake(e.rdb, broken)).Register).Router(), secret, update(78, "/start"))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// Критерий #48: повторная доставка update_id не создаёт повторного действия — весь путь
// webhook → очередь → воркер, обработчик команды вызывается один раз.
func TestRedeliveredUpdateRunsHandlerOnce(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	rt := telegram.NewRouter("TaskBot")
	rt.Command("log", func(ctx context.Context, r *telegram.Request) error {
		calls.Add(1)
		return r.Reply(ctx, "Списано")
	})
	mux := asynq.NewServeMux()
	telegram.Mount(mux, e.api, rt, e.rdb, e.jobs, nil)
	srv := queue.NewServer(e.addr, 2)
	require.NoError(t, srv.Start(mux))
	t.Cleanup(srv.Shutdown)

	h := e.router()
	for range 3 {
		require.Equal(t, http.StatusOK, post(h, secret, update(5001, "/log TM-1 1h")).Code)
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, e.tg.Calls("sendMessage"), 1)
	}, 10*time.Second, 50*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	require.EqualValues(t, 1, calls.Load())
	require.Len(t, e.tg.Calls("sendMessage"), 1)
	require.Equal(t, "Списано", e.tg.Calls("sendMessage")[0].Params["text"])
}
