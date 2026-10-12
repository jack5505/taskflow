package github_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/github"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

const secret = "webhook-secret-0123456789"

type env struct {
	db *gorm.DB
	h  http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := testutil.SharedDB(t)
	require.NoError(t, db.Exec(`DELETE FROM github_deliveries`).Error)
	require.NoError(t, db.Exec(`DELETE FROM outbox_events WHERE type = ?`, events.GitHubDelivery).Error)
	return &env{db: db, h: httpserver.New(&config.Config{Env: "test"}, db, nil).
		Mount(github.NewWebhook(secret, github.NewIntake(db)).Register).Router()}
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

// queued — payload событий github.delivery в outbox.
func (e *env) queued(t *testing.T) []github.Delivery {
	t.Helper()
	var raw []string
	require.NoError(t, e.db.Raw(`SELECT payload::text FROM outbox_events WHERE type = ? ORDER BY created_at, id`, events.GitHubDelivery).Scan(&raw).Error)
	out := make([]github.Delivery, len(raw))
	for i, r := range raw {
		require.NoError(t, json.Unmarshal([]byte(r), &out[i]))
	}
	return out
}

const prBody = `{"action":"opened","number":7,"pull_request":{"title":"TM-42: вход"}}`

// §12.2: поддельный вебхук (без подписи, чужим секретом, испорченная) отклоняется 401 и не
// обрабатывается.
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

// §12.2: подписанная доставка — 202 сразу, строка доставки и одно событие outbox; повтор того же
// X-GitHub-Delivery — тоже 202, второго события нет; другая доставка — своё событие.
func TestWebhookAcceptsDeliveryOnce(t *testing.T) {
	e := newEnv(t)
	for range 2 {
		rec := e.post(sign(prBody), "d-1", "pull_request", prBody)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		require.JSONEq(t, `{"accepted":true}`, rec.Body.String())
	}
	require.Equal(t, http.StatusAccepted, e.post(sign(prBody), "d-2", "pull_request", prBody).Code)

	q := e.queued(t)
	require.Len(t, q, 2, "d-1 принят один раз, d-2 — отдельно")
	require.Equal(t, "d-1", q[0].Delivery)
	require.Equal(t, "pull_request", q[0].Event)
	require.JSONEq(t, prBody, string(q[0].Payload))
	var n int64
	require.NoError(t, e.db.Raw(`SELECT count(*) FROM github_deliveries`).Scan(&n).Error)
	require.EqualValues(t, 2, n)
}

// Одновременные одинаковые доставки: принимается одна.
func TestWebhookConcurrentDuplicate(t *testing.T) {
	e := newEnv(t)
	done := make(chan int, 8)
	start := make(chan struct{})
	for range 8 {
		go func() {
			<-start
			done <- e.post(sign(prBody), "d-same", "pull_request", prBody).Code
		}()
	}
	close(start)
	for range 8 {
		require.Equal(t, http.StatusAccepted, <-done)
	}
	require.Len(t, e.queued(t), 1)
}

// Без id доставки или типа события, не JSON, больше 2 МБ — не принимается.
func TestWebhookRejectsBadDeliveries(t *testing.T) {
	e := newEnv(t)
	rec := e.post(sign(prBody), "", "", prBody)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), github.DeliveryHeader)
	require.Contains(t, rec.Body.String(), github.EventHeader)
	rec = e.post(sign(prBody), "d-1", "", prBody)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, rec.Body.String(), `"field":"`+github.DeliveryHeader)

	form := "payload=%7B%7D"
	rec = e.post(sign(form), "d-form", "pull_request", form)
	require.Equal(t, http.StatusBadRequest, rec.Code, "form-urlencoded вместо JSON")
	require.Contains(t, rec.Body.String(), `"code":"INVALID_JSON"`)

	big := `{"x":"` + strings.Repeat("a", 2<<20) + `"}`
	require.Equal(t, http.StatusRequestEntityTooLarge, e.post(sign(big), "d-big", "push", big).Code)
	require.Empty(t, e.queued(t))
}

// Воркер удаляет доставки старше 7 дней.
func TestCleanupDeliveries(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.db.Exec(`INSERT INTO github_deliveries (delivery_id, event, received_at) VALUES
		('old', 'ping', now() - interval '8 days'), ('new', 'ping', now() - interval '6 days')`).Error)
	n, err := github.NewIntake(e.db).CleanupDeliveries(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}
