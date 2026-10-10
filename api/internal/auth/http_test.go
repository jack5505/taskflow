package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

type httpFixture struct {
	*fixture
	r *gin.Engine
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	f := newFixture(t)
	h := auth.NewHandlers(f.svc, false)
	rdb := queue.NewRedis(testutil.SharedRedisAddr(t))
	t.Cleanup(func() { _ = rdb.Close() })
	srv := httpserver.New(&config.Config{Env: "test"}, f.db, rdb).Mount(h.Register)
	srv.Mount(func(api *gin.RouterGroup) {
		api.GET("/_test/me", h.RequireAuth(), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"email": auth.CurrentUser(c).Email})
		})
	})
	return &httpFixture{fixture: f, r: srv.Router()}
}

type req struct {
	method, path, body, bearer, cookie string
	csrf                               bool
}

func (f *httpFixture) do(t *testing.T, rq req) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(rq.method, rq.path, strings.NewReader(rq.body))
	r.Header.Set("Content-Type", "application/json")
	if rq.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+rq.bearer)
	}
	if rq.cookie != "" {
		r.AddCookie(&http.Cookie{Name: auth.RefreshCookie, Value: rq.cookie})
	}
	if rq.csrf {
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	f.r.ServeHTTP(rec, r)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func refreshCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.RefreshCookie {
			return c
		}
	}
	t.Fatal("cookie refresh_token не выставлена")
	return nil
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func TestLoginSetsHttpOnlyCookieAndReturnsAccess(t *testing.T) {
	f := newHTTPFixture(t)
	rec, body := f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"` + password + `"}`})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "Bearer", body["token_type"])
	require.NotEmpty(t, body["access_token"])
	require.NotContains(t, rec.Body.String(), "password_hash")

	c := refreshCookie(t, rec)
	require.True(t, c.HttpOnly)
	require.Equal(t, "/api/v1/auth", c.Path)
	require.Equal(t, http.SameSiteStrictMode, c.SameSite)
	require.InDelta(t, 30*24*3600, c.MaxAge, 1)

	rec, body = f.do(t, req{method: "GET", path: "/api/v1/_test/me", bearer: body["access_token"].(string)})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, f.u.Email, body["email"])
}

func TestLoginErrorsInApiFormat(t *testing.T) {
	f := newHTTPFixture(t)
	rec, body := f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"not-an-email","password":""}`})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, apierr.CodeValidation, errCode(body))

	rec, body = f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"wrong"}`})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, apierr.CodeUnauthorized, errCode(body))
	require.Empty(t, rec.Result().Cookies(), "при ошибке cookie не ставится")

	for i := 0; i < 10; i++ {
		f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"wrong"}`})
	}
	rec, body = f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"` + password + `"}`})
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, apierr.CodeRateLimited, errCode(body))
}

func TestRefreshRequiresCsrfHeaderAndRotatesCookie(t *testing.T) {
	f := newHTTPFixture(t)
	rec, _ := f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"` + password + `"}`})
	c := refreshCookie(t, rec)

	rec, body := f.do(t, req{method: "POST", path: "/api/v1/auth/refresh", cookie: c.Value})
	require.Equal(t, http.StatusForbidden, rec.Code, "без X-Requested-With — отказ (CSRF)")
	require.Equal(t, apierr.CodeForbidden, errCode(body))

	rec, body = f.do(t, req{method: "POST", path: "/api/v1/auth/refresh", cookie: c.Value, csrf: true})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, body["access_token"])
	c2 := refreshCookie(t, rec)
	require.NotEqual(t, c.Value, c2.Value, "refresh ротируется")

	rec, body = f.do(t, req{method: "POST", path: "/api/v1/auth/refresh", cookie: c.Value, csrf: true})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "старый refresh отозван")
	require.Equal(t, apierr.CodeUnauthorized, errCode(body))
	require.Equal(t, -1, refreshCookie(t, rec).MaxAge, "битая cookie очищается")

	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/refresh", csrf: true})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "без cookie")
}

func TestLogoutAndLogoutAll(t *testing.T) {
	f := newHTTPFixture(t)
	rec, body := f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"` + password + `"}`})
	c := refreshCookie(t, rec)
	access := body["access_token"].(string)

	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/logout", cookie: c.Value})
	require.Equal(t, http.StatusForbidden, rec.Code, "logout тоже требует X-Requested-With")
	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/logout", cookie: c.Value, csrf: true})
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, -1, refreshCookie(t, rec).MaxAge)
	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/refresh", cookie: c.Value, csrf: true})
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	rec2, _ := f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"` + password + `"}`})
	c2 := refreshCookie(t, rec2)
	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/logout-all"})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "без Bearer")
	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/logout-all", bearer: access})
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/refresh", cookie: c2.Value, csrf: true})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "«Выйти везде» отозвало и вторую сессию")
}

func TestRequireAuthVariants(t *testing.T) {
	f := newHTTPFixture(t)
	rec, body := f.do(t, req{method: "GET", path: "/api/v1/_test/me"})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, apierr.CodeUnauthorized, errCode(body))

	rec, _ = f.do(t, req{method: "GET", path: "/api/v1/_test/me", bearer: "garbage"})
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	_, body = f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"` + password + `"}`})
	access := body["access_token"].(string)
	require.NoError(t, f.db.Model(f.u).Update("is_active", false).Error)
	rec, body = f.do(t, req{method: "GET", path: "/api/v1/_test/me", bearer: access})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "деактивированный пользователь не проходит")
	require.Contains(t, body["error"].(map[string]any)["message"], "деактивирован")

	f.tick(time.Hour)
	require.NoError(t, f.db.Model(f.u).Update("is_active", true).Error)
	rec, _ = f.do(t, req{method: "GET", path: "/api/v1/_test/me", bearer: access})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "истёкший access-токен")
}

func TestSecureCookiesFollowsBaseURLScheme(t *testing.T) {
	require.True(t, auth.SecureCookies("https://tasks.157.173.109.181.nip.io"))
	require.True(t, auth.SecureCookies("HTTPS://example.com"))
	require.False(t, auth.SecureCookies("http://localhost:8100"))
	require.False(t, auth.SecureCookies(""))
}
