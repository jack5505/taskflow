package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

const secret = "s3cret_token-1"

// env — Redis из testcontainers (очищается перед тестом), очередь asynq и фейковый Bot API.
type env struct {
	addr string
	rdb  *redis.Client
	jobs *asynq.Client
	insp *asynq.Inspector
	tg   *testutil.FakeTelegram
	cfg  *config.Config
	api  *bot.Bot
	out  *telegram.Queue
}

func newEnv(t *testing.T) *env {
	t.Helper()
	addr := testutil.SharedRedisAddr(t)
	e := &env{addr: addr, rdb: queue.NewRedis(addr), jobs: queue.NewClient(addr), insp: asynq.NewInspector(queue.Opts(addr))}
	t.Cleanup(func() { _ = e.rdb.Close(); _ = e.jobs.Close(); _ = e.insp.Close() })
	require.NoError(t, e.rdb.FlushDB(context.Background()).Err())

	e.tg = testutil.NewFakeTelegram(t)
	e.cfg = &config.Config{TelegramBotToken: "123:TEST", TelegramAPIURL: e.tg.URL + "/", TelegramWebhookSecret: secret,
		TelegramBotUsername: "TaskBot", BaseURL: "https://tasks.example/", RedisAddr: addr}
	var err error
	e.api, err = telegram.NewBot(e.cfg)
	require.NoError(t, err)
	e.out = telegram.NewQueue(e.jobs)
	return e
}

// queued — задания типа typ в очереди (ожидающие и отложенные).
func (e *env) queued(t *testing.T, typ string) []*asynq.TaskInfo {
	t.Helper()
	var out []*asynq.TaskInfo
	pending, err := e.insp.ListPendingTasks(queue.QueueDefault, asynq.PageSize(1000))
	if err != nil && !isNoQueue(err) {
		require.NoError(t, err)
	}
	scheduled, err := e.insp.ListScheduledTasks(queue.QueueDefault, asynq.PageSize(1000))
	if err != nil && !isNoQueue(err) {
		require.NoError(t, err)
	}
	for _, ti := range append(pending, scheduled...) {
		if ti.Type == typ {
			out = append(out, ti)
		}
	}
	return out
}

// isNoQueue — очереди ещё нет: в неё ничего не ставили после очистки Redis.
func isNoQueue(err error) bool { return errors.Is(err, asynq.ErrQueueNotFound) }

// take забирает из очереди задания типа typ: возвращает их и удаляет, чтобы следующий шаг
// теста видел только новые.
func (e *env) take(t *testing.T, typ string) []*asynq.TaskInfo {
	t.Helper()
	tasks := e.queued(t, typ)
	for _, ti := range tasks {
		require.NoError(t, e.insp.DeleteTask(ti.Queue, ti.ID))
	}
	return tasks
}

// messages — сообщения из заданий telegram:send (с удалением из очереди).
func (e *env) messages(t *testing.T) []telegram.Message {
	t.Helper()
	var out []telegram.Message
	for _, ti := range e.take(t, telegram.TaskSend) {
		var m telegram.Message
		require.NoError(t, json.Unmarshal(ti.Payload, &m))
		out = append(out, m)
	}
	return out
}

// deliver отправляет сообщение через Deliverer, как воркер.
func (e *env) deliver(t *testing.T, d *telegram.Deliverer, m telegram.Message) error {
	t.Helper()
	payload, err := json.Marshal(m)
	require.NoError(t, err)
	return d.ProcessTask(context.Background(), asynq.NewTask(telegram.TaskSend, payload))
}
