package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/app"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

func TestMeProfile(t *testing.T) {
	w := newWorld(t)

	rec := w.do("GET", "/api/v1/me", w.member, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var me struct {
		Email                string
		NotificationSettings map[string]bool `json:"notification_settings"`
		Projects             []struct{ Key, Role string }
		Timer                any
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
	require.Equal(t, w.memberU.Email, me.Email)
	require.Equal(t, []struct{ Key, Role string }{{w.key, "member"}}, me.Projects)
	require.True(t, me.NotificationSettings["assigned"])
	require.Len(t, me.NotificationSettings, 10, "все ключи §8.7")
	require.Nil(t, me.Timer)
	require.NotContains(t, rec.Body.String(), "password")

	rec = w.do("PATCH", "/api/v1/me", w.member, `{"display_name":" Иван ","timezone":"Asia/Tashkent","notification_settings":{"comment":false}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var patched struct {
		DisplayName          string `json:"display_name"`
		Timezone             string
		NotificationSettings map[string]bool `json:"notification_settings"`
		Projects             []any
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &patched))
	require.Equal(t, "Иван", patched.DisplayName)
	require.Equal(t, "Asia/Tashkent", patched.Timezone)
	require.False(t, patched.NotificationSettings["comment"])
	require.True(t, patched.NotificationSettings["due"])
	require.Len(t, patched.Projects, 1, "ответ PATCH — полный профиль")

	for body, field := range map[string]string{
		`{"display_name":""}`:                          "display_name",
		`{"timezone":"Mars/Olympus"}`:                  "timezone",
		`{"notification_settings":{"telepathy":true}}`: "notification_settings",
	} {
		rec = w.do("PATCH", "/api/v1/me", w.member, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Contains(t, rec.Body.String(), `"field":"`+field+`"`, body)
	}

	rec = w.do("GET", "/api/v1/me", "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMeNotifications(t *testing.T) {
	w := newWorld(t)
	for range 3 {
		require.NoError(t, notification.Create(context.Background(), w.db, notification.New{UserID: w.memberU.ID, Type: notification.TypeMemberAdded}))
	}
	require.NoError(t, notification.Create(context.Background(), w.db, notification.New{UserID: w.managerU.ID, Type: notification.TypeMemberAdded}))

	rec := w.do("GET", "/api/v1/me/notifications?limit=2", w.member, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var page notification.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 2)
	require.EqualValues(t, 3, *page.UnreadCount)
	require.NotEmpty(t, page.NextCursor)

	rec = w.do("POST", "/api/v1/me/notifications/read", w.member, `{"ids":["`+page.Items[0].ID.String()+`"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"marked":1}`, rec.Body.String())
	r := httptest.NewRequest("POST", "/api/v1/me/notifications/read", strings.NewReader(""))
	r.ContentLength = -1 // chunked: длина неизвестна, тело пустое
	r.Header.Set("Authorization", "Bearer "+w.member)
	chunked := httptest.NewRecorder()
	w.r.ServeHTTP(chunked, r)
	require.JSONEq(t, `{"marked":2}`, chunked.Body.String(), "без тела — все")

	rec = w.do("GET", "/api/v1/me/notifications?unread=true", w.member, "")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Empty(t, page.Items)
	require.Zero(t, *page.UnreadCount)

	rec = w.do("GET", "/api/v1/me/notifications?unread=true", w.manager, "")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.EqualValues(t, 1, *page.UnreadCount, "чужие отметки не трогают")

	for q, field := range map[string]string{"unread=maybe": "unread", "limit=0": "limit", "limit=201": "limit", "cursor=@@": "cursor"} {
		rec = w.do("GET", "/api/v1/me/notifications?"+q, w.member, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, q)
		require.Contains(t, rec.Body.String(), `"field":"`+field+`"`, q)
	}
	rec = w.do("POST", "/api/v1/me/notifications/read", w.member, `{"ids":["not-uuid"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// Без TELEGRAM_BOT_TOKEN бот выключен: маршрута webhook нет, ответ 404 в формате §9.3.
func TestTelegramWebhookDisabledWithoutToken(t *testing.T) {
	w := newWorld(t)
	rec := w.do("POST", "/api/v1/telegram/webhook", "", `{"update_id":1}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"NOT_FOUND"`)
}

// С токеном и в режиме webhook маршрут есть и проверяет секрет; в long polling — нет.
func TestTelegramWebhookMountedWithToken(t *testing.T) {
	g := testutil.SharedDB(t)
	addr := testutil.SharedRedisAddr(t)
	rdb, jobs := queue.NewRedis(addr), queue.NewClient(addr)
	t.Cleanup(func() { _ = rdb.Close(); _ = jobs.Close() })
	cfg := &config.Config{Env: "test", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: time.Minute,
		TelegramBotToken: "123:abc", TelegramWebhookSecret: "secret"}

	call := func(cfg *config.Config) int {
		rec := httptest.NewRecorder()
		app.NewServer(cfg, g, rdb, jobs).Router().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/telegram/webhook", strings.NewReader(`{"update_id":1}`)))
		return rec.Code
	}
	require.Equal(t, http.StatusUnauthorized, call(cfg), "без секрета")
	polling := *cfg
	polling.TelegramPolling = true
	require.Equal(t, http.StatusNotFound, call(&polling))
}
