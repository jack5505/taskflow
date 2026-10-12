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

	st := stateOf(t, acc.ConnectURL(alice.ID))
	require.True(t, strings.HasPrefix(acc.ConnectURL(alice.ID), gh.URL+"/login/oauth/authorize?"))
	got, err := acc.Complete(ctx, "good-"+login, st)
	require.NoError(t, err)
	require.Equal(t, login, got)
	var stored *string
	require.NoError(t, e.db.Raw(`SELECT github_login::text FROM users WHERE id = ?`, alice.ID).Scan(&stored).Error)
	require.Equal(t, login, *stored)

	_, err = acc.Complete(ctx, "good-"+strings.ToUpper(login), stateOf(t, acc.ConnectURL(bob.ID)))
	require.Error(t, err, "логин уже у другого (регистр не важен)")
	require.Contains(t, err.Error(), "другим пользователем")

	for name, args := range map[string][2]string{
		"испорченный state":  {"good-x", st[:len(st)-2] + "AA"},
		"не base64":          {"good-x", "%%%"},
		"пустой код":         {"", stateOf(t, acc.ConnectURL(bob.ID))},
		"код не принят":      {"bad", stateOf(t, acc.ConnectURL(bob.ID))},
		"state чужого ключа": {"good-x", stateOf(t, github.NewAccounts(e.db, "Iv1.client", "client-secret", "other-key-0123456789abcdef0123456789", gh.URL, gh.URL, "https://tasks.example").ConnectURL(bob.ID))},
	} {
		_, err := acc.Complete(ctx, args[0], args[1])
		require.ErrorIs(t, err, github.ErrBadState, name)
	}

	require.NoError(t, acc.Unlink(ctx, alice.ID))
	require.NoError(t, e.db.Raw(`SELECT github_login::text FROM users WHERE id = ?`, alice.ID).Scan(&stored).Error)
	require.Nil(t, stored)
}

// HTTP: ссылка входа (вошедшему), возврат с github.com — переадресация в профиль с итогом.
func TestAccountHandlers(t *testing.T) {
	e := newBindEnv(t)
	gh := fakeOAuth(t)
	acc := github.NewAccounts(e.db, "Iv1.client", "client-secret", "state-key-0123456789abcdef0123456789", gh.URL, gh.URL, "https://tasks.example")
	u := e.user(t, false)
	as := func(c *gin.Context) { c.Set("auth.user", u) }
	h := httpserver.New(&config.Config{Env: "test"}, e.db, nil).Mount(github.NewAccountHandlers(acc, as).Register).Router()
	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	rec := do("GET", "/api/v1/me/github/connect")
	require.Equal(t, http.StatusOK, rec.Code)
	var link github.ConnectLink
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &link))
	st := stateOf(t, link.URL)

	login := "u" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	rec = do("GET", "/api/v1/github/callback?code=good-"+login+"&state="+url.QueryEscape(st))
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "https://tasks.example/profile?github=linked", rec.Header().Get("Location"))

	rec = do("GET", "/api/v1/github/callback?code=bad&state="+url.QueryEscape(st))
	require.Equal(t, http.StatusFound, rec.Code)
	loc, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "error", loc.Query().Get("github"))
	require.NotEmpty(t, loc.Query().Get("message"))

	require.Equal(t, http.StatusNoContent, do("DELETE", "/api/v1/me/github").Code)
	var fresh user.User
	require.NoError(t, e.db.First(&fresh, "id = ?", u.ID).Error)
	require.Nil(t, fresh.GitHubLogin)
}
