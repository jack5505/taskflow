package github_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/github"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

// HTTP: GET/PUT/DELETE /projects/{key}/github и GET /github/repositories; права по §12.6.
func TestHandlers(t *testing.T) {
	e := newBindEnv(t)
	m, member := e.user(t, false), e.user(t, false)
	p := e.project(t, map[*user.User]string{m: project.RoleManager, member: project.RoleMember})
	var current *user.User
	as := func(c *gin.Context) { c.Set("auth.user", current) } // requireAuth: пользователь уже вошёл
	h := httpserver.New(&config.Config{Env: "test"}, e.db, nil).
		Mount(github.NewHandlers(e.svc, project.NewService(e.db), as).Register).Router()
	do := func(u *user.User, method, path, body string) *httptest.ResponseRecorder {
		current = u
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, r)
		return rec
	}
	base := "/api/v1/projects/" + p.Key + "/github"

	rec := do(member, "GET", base, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"repo":null,"issues":false}`, rec.Body.String())
	require.Equal(t, http.StatusForbidden, do(member, "PUT", base, `{"repo":"acme/backend"}`).Code)
	require.Equal(t, http.StatusBadRequest, do(m, "PUT", base, `{}`).Code)
	rec = do(m, "PUT", base, `{"repo":"acme/backend","issues":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"repo":"acme/backend","issues":true}`, rec.Body.String())
	require.JSONEq(t, `{"repo":"acme/backend","issues":true}`, do(member, "GET", base, "").Body.String())
	require.Equal(t, http.StatusForbidden, do(member, "DELETE", base, "").Code)
	require.Equal(t, http.StatusNoContent, do(m, "DELETE", base, "").Code)
	require.Equal(t, http.StatusNotFound, do(e.user(t, false), "GET", base, "").Code, "не участник — 404")

	require.Equal(t, http.StatusOK, do(m, "GET", "/api/v1/github/repositories", "").Code)
	require.Equal(t, http.StatusForbidden, do(e.user(t, false), "GET", "/api/v1/github/repositories", "").Code)
}
