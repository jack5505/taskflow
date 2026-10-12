package github_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/github"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/user"
)

// fakeOAuth — github.com (вход) и /user API: код "good-<логин>" даёт токен этого логина.
func fakeOAuth(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			require.NoError(t, r.ParseForm())
			if r.Form.Get("client_id") != "Iv1.client" || r.Form.Get("client_secret") != "client-secret" {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "incorrect_client_credentials"})
				return
			}
			login, ok := strings.CutPrefix(r.Form.Get("code"), "good-")
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "bad_verification_code"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "user-token-" + login})
		case "/user":
			login, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer user-token-")
			if !ok {
				http.Error(w, "{}", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"login": login})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stateOf — state из ссылки входа.
func stateOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	require.NoError(t, err)
	require.Equal(t, "Iv1.client", u.Query().Get("client_id"))
	require.Equal(t, "https://tasks.example/api/v1/github/callback", u.Query().Get("redirect_uri"))
	return u.Query().Get("state")
}

// §12.5: пользователь получает ссылку входа, GitHub возвращает его с кодом — логин записан;
// чужой state, испорченный, без кода — нет; тот же логин у другого — ошибка; отвязка.
func TestAccountLinking(t *testing.T) {
	e := newBindEnv(t)
	gh := fakeOAuth(t)
	acc := github.NewAccounts(e.db, "Iv1.client", "client-secret", "state-key-0123456789abcdef0123456789", gh.URL, gh.URL, "https://tasks.example/")
	ctx := context.Background()
	alice, bob := e.user(t, false), e.user(t, false)
	login := "alice-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")

	link, nonce := acc.ConnectURL(alice.ID)
	st := stateOf(t, link)
	require.True(t, strings.HasPrefix(link, gh.URL+"/login/oauth/authorize?"))
	_, otherNonce := acc.ConnectURL(alice.ID)
	_, err := acc.Complete(ctx, "good-"+login, st, otherNonce)
	require.ErrorIs(t, err, github.ErrBadState, "state из чужого браузера (CSRF)")
	_, err = acc.Complete(ctx, "good-"+login, st, "")
	require.ErrorIs(t, err, github.ErrBadState, "без cookie браузера")
	got, err := acc.Complete(ctx, "good-"+login, st, nonce)
	require.NoError(t, err)
	require.Equal(t, login, got)
	var stored *string
	require.NoError(t, e.db.Raw(`SELECT github_login::text FROM users WHERE id = ?`, alice.ID).Scan(&stored).Error)
	require.Equal(t, login, *stored)

	bobLink, bobNonce := acc.ConnectURL(bob.ID)
	_, err = acc.Complete(ctx, "good-"+strings.ToUpper(login), stateOf(t, bobLink), bobNonce)
	require.Error(t, err, "логин уже у другого (регистр не важен)")
	require.Contains(t, err.Error(), "другим пользователем")

	foreign := github.NewAccounts(e.db, "Iv1.client", "client-secret", "other-key-0123456789abcdef0123456789", gh.URL, gh.URL, "https://tasks.example")
	foreignLink, foreignNonce := foreign.ConnectURL(bob.ID)
	for name, args := range map[string][3]string{
		"испорченный state":  {"good-x", st[:len(st)-2] + "AA", nonce},
		"не base64":          {"good-x", "%%%", nonce},
		"пустой код":         {"", stateOf(t, bobLink), bobNonce},
		"код не принят":      {"bad", stateOf(t, bobLink), bobNonce},
		"state чужого ключа": {"good-x", stateOf(t, foreignLink), foreignNonce},
	} {
		_, err := acc.Complete(ctx, args[0], args[1], args[2])
		require.ErrorIs(t, err, github.ErrBadState, name)
	}

	// Деактивированному — нельзя.
	require.NoError(t, e.db.Model(&user.User{}).Where("id = ?", bob.ID).Update("is_active", false).Error)
	_, err = acc.Complete(ctx, "good-bob"+login, stateOf(t, bobLink), bobNonce)
	require.Error(t, err)
	require.Contains(t, err.Error(), "деактивирован")

	require.NoError(t, acc.Unlink(ctx, alice.ID))
	require.NoError(t, e.db.Raw(`SELECT github_login::text FROM users WHERE id = ?`, alice.ID).Scan(&stored).Error)
	require.Nil(t, stored)
	var actions []string
	require.NoError(t, e.db.Raw(`SELECT action FROM audit_logs WHERE entity_id = ? ORDER BY created_at`, alice.ID.String()).Scan(&actions).Error)
	require.Equal(t, []string{github.AuditAccountLinked, github.AuditAccountUnlinked}, actions)
}

// HTTP: ссылка входа (вошедшему), возврат с github.com — переадресация в профиль с итогом.
func TestAccountHandlers(t *testing.T) {
	e := newBindEnv(t)
	gh := fakeOAuth(t)
	acc := github.NewAccounts(e.db, "Iv1.client", "client-secret", "state-key-0123456789abcdef0123456789", gh.URL, gh.URL, "https://tasks.example")
	u := e.user(t, false)
	as := func(c *gin.Context) { c.Set("auth.user", u) }
	h := httpserver.New(&config.Config{Env: "test"}, e.db, nil).Mount(github.NewAccountHandlers(acc, as, true, true).Register).Router()
	do := func(method, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		h.ServeHTTP(rec, r)
		return rec
	}
	connect := func() (string, *http.Cookie) {
		rec := do("GET", "/api/v1/me/github/connect")
		require.Equal(t, http.StatusOK, rec.Code)
		var link github.ConnectLink
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &link))
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		require.True(t, cookies[0].HttpOnly)
		require.True(t, cookies[0].Secure)
		require.Equal(t, "/api/v1/github/callback", cookies[0].Path)
		return stateOf(t, link.URL), cookies[0]
	}
	reason := func(rec *httptest.ResponseRecorder) string {
		require.Equal(t, http.StatusFound, rec.Code)
		loc, err := url.Parse(rec.Header().Get("Location"))
		require.NoError(t, err)
		require.Equal(t, "error", loc.Query().Get("github"))
		return loc.Query().Get("reason")
	}

	st, cookie := connect()
	login := "u" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	require.Equal(t, "state", reason(do("GET", "/api/v1/github/callback?code=good-"+login+"&state="+url.QueryEscape(st))),
		"без cookie браузера — чужая ссылка (CSRF)")
	rec := do("GET", "/api/v1/github/callback?code=good-"+login+"&state="+url.QueryEscape(st), cookie)
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "https://tasks.example/profile?github=linked", rec.Header().Get("Location"))
	cleared := rec.Result().Cookies()
	require.Len(t, cleared, 1)
	require.Negative(t, cleared[0].MaxAge, "cookie снимается: одна попытка на ссылку")

	st, cookie = connect()
	require.Equal(t, "state", reason(do("GET", "/api/v1/github/callback?code=bad&state="+url.QueryEscape(st), cookie)))

	require.Equal(t, http.StatusNoContent, do("DELETE", "/api/v1/me/github").Code)
	var fresh user.User
	require.NoError(t, e.db.First(&fresh, "id = ?", u.ID).Error)
	require.Nil(t, fresh.GitHubLogin)
}
