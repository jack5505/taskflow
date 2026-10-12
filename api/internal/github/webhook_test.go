package github_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/github"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

const secret = "webhook-secret-0123456789"

type env struct {
	rdb  *redis.Client
	jobs *asynq.Client
	insp *asynq.Inspector
	h    http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	addr := testutil.SharedRedisAddr(t)
	e := &env{rdb: queue.NewRedis(addr), jobs: queue.NewClient(addr), insp: asynq.NewInspector(queue.Opts(addr))}
	t.Cleanup(func() { _ = e.rdb.Close(); _ = e.jobs.Close(); _ = e.insp.Close() })
	require.NoError(t, e.rdb.FlushDB(context.Background()).Err())
	e.h = httpserver.New(&config.Config{Env: "test"}, nil, e.rdb).
		Mount(github.NewWebhook(secret, github.NewIntake(e.rdb, e.jobs)).Register).Router()
	return e
}

func sign(body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (e *env) post(signature, delivery, event, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/github/webhook", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range map[string]string{github.SignatureHeader: signature, github.DeliveryHeader: delivery, github.EventHeader: event} {
		if v != "" {
			r.Header.Set(k, v)
		}
	}
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *env) queued(t *testing.T) []*asynq.TaskInfo {
	t.Helper()
	pending, err := e.insp.ListPendingTasks(queue.QueueDefault, asynq.PageSize(1000))
	if err != nil && !errors.Is(err, asynq.ErrQueueNotFound) {
		require.NoError(t, err)
	}
	var out []*asynq.TaskInfo
	for _, ti := range pending {
		if ti.Type == github.TaskEvent {
			out = append(out, ti)
		}
	}
	return out
}

const prBody = `{"action":"opened","number":7,"pull_request":{"title":"TM-42: вход"}}`

// §12.2: поддельный вебхук (без подписи, чужим секретом, испорченная) отклоняется 401 и не
// ставится в очередь.
func TestWebhookRejectsForgedSignature(t *testing.T) {
	e := newEnv(t)
	other := hmac.New(sha256.New, []byte("other-secret-0123456789"))
	other.Write([]byte(prBody))
	for name, sig := range map[string]string{
		"нет подписи":  "",
		"чужой секрет": "sha256=" + hex.EncodeToString(other.Sum(nil)),
		"без префикса": strings.TrimPrefix(sign(prBody), "sha256="),
		"не hex":       "sha256=zz",
		"подпись sha1": "sha1=" + strings.TrimPrefix(sign(prBody), "sha256="),
		"другое тело":  sign(prBody + " "),
	} {
		rec := e.post(sig, "d-1", "pull_request", prBody)
		require.Equal(t, http.StatusUnauthorized, rec.Code, name)
		require.Contains(t, rec.Body.String(), `"code":"UNAUTHORIZED"`, name)
	}
	require.Empty(t, e.queued(t), "поддельные доставки не обрабатываются")
}

// §12.2: подписанная доставка — 202 сразу и одно задание; повтор того же X-GitHub-Delivery —
// тоже 202, но второго задания нет; другая доставка — своё задание.
func TestWebhookAcceptsDeliveryOnce(t *testing.T) {
	e := newEnv(t)
	for range 2 {
		rec := e.post(sign(prBody), "d-1", "pull_request", prBody)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		require.JSONEq(t, `{"accepted":true}`, rec.Body.String())
	}
	require.Equal(t, http.StatusAccepted, e.post(sign(prBody), "d-2", "pull_request", prBody).Code)

	tasks := e.queued(t)
	require.Len(t, tasks, 2, "d-1 принят один раз, d-2 — отдельно")
	var job struct {
		Event      string          `json:"event"`
		Delivery   string          `json:"delivery"`
		ReceivedAt time.Time       `json:"received_at"`
		Payload    json.RawMessage `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(tasks[0].Payload, &job))
	require.Equal(t, "pull_request", job.Event)
	require.Contains(t, []string{"d-1", "d-2"}, job.Delivery)
	require.JSONEq(t, prBody, string(job.Payload))
	require.WithinDuration(t, time.Now(), job.ReceivedAt, time.Minute)

	ttl, err := e.rdb.TTL(context.Background(), "gh:delivery:d-1").Result()
	require.NoError(t, err)
	require.InDelta(t, (7 * 24 * time.Hour).Seconds(), ttl.Seconds(), 60, "доставка помнится 7 дней (§12.8)")
}

// Без id доставки или типа события доставка не принимается.
func TestWebhookRequiresDeliveryAndEvent(t *testing.T) {
	e := newEnv(t)
	require.Equal(t, http.StatusBadRequest, e.post(sign(prBody), "", "pull_request", prBody).Code)
	require.Equal(t, http.StatusBadRequest, e.post(sign(prBody), "d-1", "", prBody).Code)
	require.Empty(t, e.queued(t))
}

// Тело больше 25 МБ — 413.
func TestWebhookTooLarge(t *testing.T) {
	e := newEnv(t)
	big := `{"x":"` + strings.Repeat("a", 25<<20) + `"}`
	require.Equal(t, http.StatusRequestEntityTooLarge, e.post(sign(big), "d-big", "push", big).Code)
}

// Redis недоступен — 500: GitHub повторит доставку; после восстановления она принимается.
func TestWebhookRedisDown(t *testing.T) {
	e := newEnv(t)
	broken := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = broken.Close() })
	h := httpserver.New(&config.Config{Env: "test"}, nil, e.rdb).
		Mount(github.NewWebhook(secret, github.NewIntake(broken, e.jobs)).Register).Router()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/github/webhook", strings.NewReader(prBody))
	r.Header.Set(github.SignatureHeader, sign(prBody))
	r.Header.Set(github.DeliveryHeader, "d-9")
	r.Header.Set(github.EventHeader, "pull_request")
	h.ServeHTTP(rec, r)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, http.StatusAccepted, e.post(sign(prBody), "d-9", "pull_request", prBody).Code)
}

// Обработчик задания разбирает событие; битое задание не повторяется.
func TestProcessorHandle(t *testing.T) {
	mux := asynq.NewServeMux()
	github.Mount(mux, github.NewProcessor())
	ok, _ := json.Marshal(map[string]any{"event": "ping", "delivery": "d-1", "payload": json.RawMessage(`{}`)})
	require.NoError(t, mux.ProcessTask(context.Background(), asynq.NewTask(github.TaskEvent, ok)))
	err := mux.ProcessTask(context.Background(), asynq.NewTask(github.TaskEvent, []byte("{")))
	require.ErrorIs(t, err, asynq.SkipRetry)
}
