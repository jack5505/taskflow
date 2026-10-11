package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TelegramCall — один запрос к фейковому Bot API: метод и поля формы.
type TelegramCall struct {
	Method string
	Params map[string]string
	At     time.Time
}

// FakeTelegram — фейковый Bot API на httptest: записывает вызовы и отвечает успехом либо
// заранее заданными ответами. Настоящий api.telegram.org в тестах не вызывается.
type FakeTelegram struct {
	*httptest.Server
	mu      sync.Mutex
	calls   []TelegramCall
	queued  map[string][]string
	nextMsg int
	delay   map[string]time.Duration
}

// NewFakeTelegram поднимает сервер на время теста. URL — значение TELEGRAM_API_URL.
func NewFakeTelegram(t *testing.T) *FakeTelegram {
	t.Helper()
	f := &FakeTelegram{queued: map[string][]string{}, delay: map[string]time.Duration{}, nextMsg: 100}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// Respond задаёт ответ на следующий вызов method (JSON целиком, например ответ 429).
func (f *FakeTelegram) Respond(method, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued[method] = append(f.queued[method], body)
}

// TooManyRequests — ответ 429 с retry_after в секундах.
func TooManyRequests(retryAfter int) string {
	return fmt.Sprintf(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after %d","parameters":{"retry_after":%d}}`, retryAfter, retryAfter)
}

// Delay задерживает ответы на method (медленный Telegram).
func (f *FakeTelegram) Delay(method string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay[method] = d
}

// Calls — вызовы method по порядку; пустой method — все вызовы.
func (f *FakeTelegram) Calls(method string) []TelegramCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []TelegramCall
	for _, c := range f.calls {
		if method == "" || c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *FakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	// Путь /bot<token>/<method>.
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	params := map[string]string{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			for k, v := range r.MultipartForm.Value {
				params[k] = v[0]
			}
		}
	} else {
		_, _ = io.Copy(io.Discard, r.Body)
	}

	f.mu.Lock()
	f.calls = append(f.calls, TelegramCall{Method: method, Params: params, At: time.Now()})
	d := f.delay[method]
	var body string
	if q := f.queued[method]; len(q) > 0 {
		body, f.queued[method] = q[0], q[1:]
	} else {
		body = f.success(method, params)
	}
	f.mu.Unlock()

	if d > 0 {
		time.Sleep(d)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

// success — ответ ok: сообщение с новым message_id для sendMessage, пустой список для
// getUpdates, true для остального.
func (f *FakeTelegram) success(method string, params map[string]string) string {
	switch method {
	case "getUpdates":
		return `{"ok":true,"result":[]}`
	case "sendMessage":
	default:
		return `{"ok":true,"result":true}`
	}
	f.nextMsg++
	var chatID int64
	_ = json.Unmarshal([]byte(params["chat_id"]), &chatID)
	msg, _ := json.Marshal(map[string]any{"message_id": f.nextMsg, "date": time.Now().Unix(),
		"chat": map[string]any{"id": chatID, "type": "private"}, "text": params["text"]})
	return `{"ok":true,"result":` + string(msg) + `}`
}
