package users_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
	"github.com/jack5505/taskflow/api/internal/users"
)

const password = "correct horse battery"

var passwordHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(password)
	if err != nil {
		panic(err)
	}
	return h
})

type fixture struct {
	db   *gorm.DB
	auth *auth.Service
	svc  *users.Service
	r    http.Handler
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	g := testutil.SharedDB(t)
	addr := testutil.SharedRedisAddr(t)
	rdb := queue.NewRedis(addr)
	jobs := queue.NewClient(addr)
	t.Cleanup(func() { _ = rdb.Close(); _ = jobs.Close() })

	cfg := &config.Config{BaseURL: "https://tasks.example", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: 15 * time.Minute, JWTRefreshTTL: time.Hour}
	authSvc := auth.NewService(cfg, g, rdb).WithQueue(jobs)
	svc := users.NewService(g, authSvc)
	authH := auth.NewHandlers(authSvc, false)
	srv := httpserver.New(&config.Config{Env: "test"}, g, rdb).Mount(authH.Register).
		Mount(users.NewHandlers(svc, authH.RequireAuth()).Register)
	return &fixture{db: g, auth: authSvc, svc: svc, r: srv.Router()}
}

func (f *fixture) user(t *testing.T, admin bool) *user.User {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), passwordHash(), "Пользователь")
	u.IsSystemAdmin = admin
	require.NoError(t, f.db.Create(u).Error)
	return u
}

func (f *fixture) login(t *testing.T, u *user.User) *auth.Session {
	t.Helper()
	s, err := f.auth.Login(context.Background(), u.Email, password, "")
	require.NoError(t, err)
	return s
}

func (f *fixture) project(t *testing.T, managers ...*user.User) *project.Project {
	t.Helper()
	p := project.New(testutil.UniqueKey(t), "Проект")
	require.NoError(t, f.db.Create(p).Error)
	for _, m := range managers {
		require.NoError(t, f.db.Create(&project.Member{ProjectID: p.ID, UserID: m.ID, Role: project.RoleManager}).Error)
	}
	return p
}

func (f *fixture) do(t *testing.T, method, path, token, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	f.r.ServeHTTP(rec, r)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func fieldOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Details struct{ Fields []apierr.FieldError }
		}
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Error.Details.Fields, 1, rec.Body.String())
	return body.Error.Details.Fields[0].Field
}

func pendingInvites(t *testing.T, userID string) int {
	t.Helper()
	insp := asynq.NewInspector(queue.Opts(testutil.SharedRedisAddr(t)))
	defer func() { _ = insp.Close() }()
	tasks, err := insp.ListPendingTasks(queue.QueueDefault, asynq.PageSize(1000))
	require.NoError(t, err)
	n := 0
	for _, tk := range tasks {
		if tk.Type == auth.TaskPasswordInvite && strings.Contains(string(tk.Payload), userID) {
			n++
		}
	}
	return n
}

func TestCreateUser(t *testing.T) {
	f := newFixture(t)
	admin := f.login(t, f.user(t, true)).AccessToken
	email := testutil.UniqueEmail(t)

	rec, body := f.do(t, "POST", "/api/v1/users", admin, `{"email":"`+email+`","display_name":" Анна Смирнова "}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "Анна Смирнова", body["display_name"])
	require.Equal(t, "Europe/Moscow", body["timezone"])
	require.Equal(t, false, body["is_system_admin"])
	require.NotContains(t, rec.Body.String(), "password")
	id := body["id"].(string)
	require.Equal(t, 1, pendingInvites(t, id), "письмо-приглашение поставлено воркеру")

	var u user.User
	require.NoError(t, f.db.First(&u, "id = ?", id).Error)
	require.False(t, auth.HasPassword(&u), "пароль задаётся по ссылке")

	rec, _ = f.do(t, "POST", "/api/v1/users", admin, `{"email":"`+strings.ToUpper(email)+`","display_name":"Дубль"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "email", fieldOf(t, rec), "email без учёта регистра уже занят")

	rec, _ = f.do(t, "POST", "/api/v1/users", admin, `{"email":"`+testutil.UniqueEmail(t)+`","display_name":"X","timezone":"Mars/Base"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "timezone", fieldOf(t, rec))

	rec, _ = f.do(t, "POST", "/api/v1/users", admin, `{"email":"`+testutil.UniqueEmail(t)+`","display_name":"   "}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "display_name", fieldOf(t, rec))

	rec, body = f.do(t, "POST", "/api/v1/users", admin, `{"email":"bad"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, apierr.CodeValidation, errCode(body))
}

func TestUsersRights(t *testing.T) {
	f := newFixture(t)
	manager := f.user(t, false)
	f.project(t, manager)
	member := f.user(t, false)
	p := f.project(t, f.user(t, false))
	require.NoError(t, f.db.Create(&project.Member{ProjectID: p.ID, UserID: member.ID, Role: project.RoleMember}).Error)

	rec, _ := f.do(t, "GET", "/api/v1/users", "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	rec, _ = f.do(t, "GET", "/api/v1/users", f.login(t, f.user(t, true)).AccessToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), manager.Email)

	managerToken := f.login(t, manager).AccessToken
	rec, _ = f.do(t, "GET", "/api/v1/users", managerToken, "")
	require.Equal(t, http.StatusOK, rec.Code, "менеджер видит список для добавления в проект")

	memberToken := f.login(t, member).AccessToken
	rec, body := f.do(t, "GET", "/api/v1/users", memberToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, apierr.CodeForbidden, errCode(body))

	for _, tok := range []string{managerToken, memberToken} {
		rec, _ = f.do(t, "POST", "/api/v1/users", tok, `{"email":"`+testutil.UniqueEmail(t)+`","display_name":"X"}`)
		require.Equal(t, http.StatusForbidden, rec.Code, "создавать — только администратор")
		rec, _ = f.do(t, "PATCH", "/api/v1/users/"+member.ID.String(), tok, `{"is_active":false}`)
		require.Equal(t, http.StatusForbidden, rec.Code, "деактивировать — только администратор")
	}

	require.NoError(t, f.db.Delete(&project.Project{}, "id IN (SELECT project_id FROM project_members WHERE user_id = ?)", manager.ID).Error)
	rec, _ = f.do(t, "GET", "/api/v1/users", managerToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code, "менеджер только удалённого проекта — не менеджер")
}

func TestUpdateUser(t *testing.T) {
	f := newFixture(t)
	admin := f.login(t, f.user(t, true)).AccessToken
	u := f.user(t, false)
	path := "/api/v1/users/" + u.ID.String()

	rec, body := f.do(t, "PATCH", path, admin, `{"display_name":"Новое имя","timezone":"Asia/Tashkent","is_system_admin":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "Новое имя", body["display_name"])
	require.Equal(t, "Asia/Tashkent", body["timezone"])
	require.Equal(t, true, body["is_system_admin"])

	rec, _ = f.do(t, "PATCH", path, admin, `{"timezone":"Local"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "timezone", fieldOf(t, rec))

	rec, body = f.do(t, "PATCH", "/api/v1/users/"+testutil.UniqueKey(t), admin, `{}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, apierr.CodeNotFound, errCode(body))
	rec, _ = f.do(t, "PATCH", "/api/v1/users/00000000-0000-4000-8000-000000000000", admin, `{"display_name":"X"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// §3.1: деактивированный не может войти, сессии отозваны, привязка Telegram снята,
// в outbox событие user.deactivated; повторная активация возвращает вход.
func TestDeactivateAndReactivate(t *testing.T) {
	f := newFixture(t)
	admin := f.login(t, f.user(t, true)).AccessToken
	u := f.user(t, false)
	tgID, tgName := int64(1000+testutil.Next()), "ivan"
	require.NoError(t, f.db.Model(u).Updates(map[string]any{"telegram_user_id": tgID, "telegram_username": tgName}).Error)
	s := f.login(t, u)
	f.project(t, u, f.user(t, false)) // менеджер, но не единственный

	rec, body := f.do(t, "PATCH", "/api/v1/users/"+u.ID.String(), admin, `{"is_active":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, false, body["is_active"])
	require.Nil(t, body["telegram_user_id"])
	require.Nil(t, body["telegram_username"])

	_, err := f.auth.Refresh(context.Background(), s.RefreshToken, "")
	require.Error(t, err, "старый refresh не продлевает сессию")
	_, err = f.auth.Authenticate(context.Background(), s.AccessToken)
	require.ErrorIs(t, err, auth.ErrUserInactive)
	_, err = f.auth.Login(context.Background(), u.Email, password, "")
	require.ErrorIs(t, err, auth.ErrUserInactive)

	var n int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'user.deactivated' AND payload->>'user_id' = ?`, u.ID.String()).Scan(&n).Error)
	require.EqualValues(t, 1, n)

	rec, _ = f.do(t, "PATCH", "/api/v1/users/"+u.ID.String(), admin, `{"is_active":false}`)
	require.Equal(t, http.StatusOK, rec.Code, "повторная деактивация — без нового события")
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'user.deactivated' AND payload->>'user_id' = ?`, u.ID.String()).Scan(&n).Error)
	require.EqualValues(t, 1, n)

	rec, body = f.do(t, "PATCH", "/api/v1/users/"+u.ID.String(), admin, `{"is_active":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, true, body["is_active"])
	f.login(t, u)
}

// Критерий #12: деактивация единственного менеджера отклоняется (409 LAST_MANAGER).
func TestDeactivateLastManager(t *testing.T) {
	f := newFixture(t)
	admin := f.login(t, f.user(t, true)).AccessToken
	u := f.user(t, false)
	inactive := f.user(t, false)
	require.NoError(t, f.db.Model(inactive).Update("is_active", false).Error)
	p := f.project(t, u, inactive)
	deleted := f.project(t, u)
	require.NoError(t, f.db.Delete(deleted).Error)

	rec, body := f.do(t, "PATCH", "/api/v1/users/"+u.ID.String(), admin, `{"is_active":false,"display_name":"Не применится"}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Equal(t, apierr.CodeLastManager, errCode(body))
	details := body["error"].(map[string]any)["details"].(map[string]any)
	require.Equal(t, []any{p.Key}, details["projects"], "деактивированный второй менеджер не считается, удалённый проект не мешает")

	var got user.User
	require.NoError(t, f.db.First(&got, "id = ?", u.ID).Error)
	require.True(t, got.IsActive)
	require.Equal(t, "Пользователь", got.DisplayName, "при отказе ничего не меняется")

	require.NoError(t, f.db.Create(&project.Member{ProjectID: p.ID, UserID: f.user(t, false).ID, Role: project.RoleManager}).Error)
	rec, _ = f.do(t, "PATCH", "/api/v1/users/"+u.ID.String(), admin, `{"is_active":false}`)
	require.Equal(t, http.StatusOK, rec.Code, "после назначения второго менеджера — можно")
}

// Два менеджера одного проекта деактивируются одновременно: проходит ровно один.
func TestConcurrentDeactivationKeepsOneManager(t *testing.T) {
	f := newFixture(t)
	a, b := f.user(t, false), f.user(t, false)
	f.project(t, a, b)

	off := false
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, u := range []*user.User{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.svc.Update(context.Background(), u.ID, users.UpdateInput{IsActive: &off})
		}()
	}
	wg.Wait()

	failed := 0
	for _, err := range errs {
		if err != nil {
			require.ErrorIs(t, err, users.ErrLastManager)
			failed++
		}
	}
	require.Equal(t, 1, failed, "ровно одна деактивация отклонена")
}
