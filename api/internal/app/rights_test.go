package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/app"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

const password = "correct horse battery"

var passwordHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(password)
	if err != nil {
		panic(err)
	}
	return h
})

// world — роли §2 вокруг одного проекта: администратор системы, менеджер, исполнитель
// и пользователь без роли в проекте.
type world struct {
	db                               *gorm.DB
	r                                http.Handler
	admin, manager, member, outsider string // access-токены
	managerU, memberU                *user.User
	key                              string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	g := testutil.SharedDB(t)
	addr := testutil.SharedRedisAddr(t)
	rdb := queue.NewRedis(addr)
	jobs := queue.NewClient(addr)
	t.Cleanup(func() { _ = rdb.Close(); _ = jobs.Close() })
	cfg := &config.Config{Env: "test", BaseURL: "http://localhost", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: 15 * time.Minute, JWTRefreshTTL: time.Hour}
	authSvc := auth.NewService(cfg, g, rdb)
	w := &world{db: g, r: app.NewServer(cfg, g, rdb, jobs).Router()}

	mk := func(admin bool) (*user.User, string) {
		u := user.New(testutil.UniqueEmail(t), passwordHash(), "Пользователь")
		u.IsSystemAdmin = admin
		require.NoError(t, g.Create(u).Error)
		s, err := authSvc.Login(context.Background(), u.Email, password, "")
		require.NoError(t, err)
		return u, s.AccessToken
	}
	_, w.admin = mk(true)
	w.managerU, w.manager = mk(false)
	w.memberU, w.member = mk(false)
	_, w.outsider = mk(false)

	p := project.New(testutil.UniqueKey(t), "Проект")
	require.NoError(t, g.Create(p).Error)
	require.NoError(t, g.Create(&project.Member{ProjectID: p.ID, UserID: w.managerU.ID, Role: project.RoleManager}).Error)
	require.NoError(t, g.Create(&project.Member{ProjectID: p.ID, UserID: w.memberU.ID, Role: project.RoleMember}).Error)
	w.key = p.Key
	return w
}

// user — ещё один активный пользователь без роли; возвращает id.
func (w *world) user(t *testing.T) string {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), passwordHash(), "Новичок")
	require.NoError(t, w.db.Create(u).Error)
	return u.ID.String()
}

func (w *world) do(method, path, token, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w.r.ServeHTTP(rec, r)
	return rec
}

// Таблица §2: каждая ячейка «Нет» по существующим маршрутам даёт 403, а пользователь без роли
// в проекте не видит проект — 404. Новые маршруты (задачи, спринты, время, отчёты) добавляют
// свои строки сюда.
func TestRightsTable(t *testing.T) {
	w := newWorld(t)
	proj := "/api/v1/projects/" + w.key
	member := w.memberU.ID.String()

	type row struct {
		action             string
		method, path, body string
	}
	no := map[string][]row{ // кому «Нет» → 403
		"manager": {
			{"Создавать проекты", "POST", "/api/v1/projects", `{"key":"ZZQ","name":"X"}`},
			{"Удалять проекты", "DELETE", proj, ""},
			{"Восстанавливать проект", "POST", proj + "/restore", ""},
			{"Архивировать проект", "PATCH", proj, `{"is_archived":true}`},
			{"Смотреть корзину (удалённые задачи)", "GET", proj + "/tasks?include_deleted=true", ""},
			{"Заводить пользователей", "POST", "/api/v1/users", `{"email":"x@example.com","display_name":"X"}`},
			{"Деактивировать пользователей", "PATCH", "/api/v1/users/" + member, `{"is_active":false}`},
		},
		"member": {
			{"Создавать проекты", "POST", "/api/v1/projects", `{"key":"ZZQ","name":"X"}`},
			{"Удалять проекты", "DELETE", proj, ""},
			{"Восстанавливать проект", "POST", proj + "/restore", ""},
			{"Архивировать проект", "PATCH", proj, `{"is_archived":true}`},
			{"Приглашать пользователей в проект", "PUT", proj + "/members/" + w.managerU.ID.String(), `{"role":"member"}`},
			{"Менять роли", "PUT", proj + "/members/" + member, `{"role":"manager"}`},
			{"Исключать из проекта", "DELETE", proj + "/members/" + member, ""},
			{"Настраивать уведомления бота для проекта", "PATCH", proj, `{"notification_settings":{"daily_summary":true}}`},
			{"Менять настройки проекта", "PATCH", proj, `{"name":"X"}`},
			{"Заводить пользователей", "POST", "/api/v1/users", `{"email":"x@example.com","display_name":"X"}`},
			{"Деактивировать пользователей", "PATCH", "/api/v1/users/" + member, `{"is_active":false}`},
			{"Список пользователей (только админ и менеджер)", "GET", "/api/v1/users", ""},
			{"Назначать исполнителя (при создании задачи)", "POST", proj + "/tasks", `{"title":"X","assignee_id":"` + member + `"}`},
			{"Смотреть корзину (удалённые задачи)", "GET", proj + "/tasks?include_deleted=true", ""},
		},
	}
	tokens := map[string]string{"manager": w.manager, "member": w.member}
	for who, rows := range no {
		for _, r := range rows {
			t.Run(fmt.Sprintf("%s/%s", who, r.action), func(t *testing.T) {
				rec := w.do(r.method, r.path, tokens[who], r.body)
				require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			})
		}
	}

	// Задача проекта для строк про задачи.
	rec := w.do("POST", proj+"/tasks", w.member, `{"title":"Задача"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct{ Key string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	taskPath := "/api/v1/tasks/" + created.Key
	for who, token := range map[string]string{"manager": w.manager, "member": w.member} {
		rec := w.do("POST", taskPath+"/restore", token, "")
		require.Equal(t, http.StatusForbidden, rec.Code, "%s: восстанавливать задачи — только администратор", who)
	}
	rec = w.do("DELETE", taskPath, w.member, "")
	require.Equal(t, http.StatusForbidden, rec.Code, "исполнитель: удалять задачи — нет")

	// Пользователь без роли в проекте не видит его ни через один маршрут проекта — 404.
	for _, r := range []row{
		{"Создание задачи", "POST", proj + "/tasks", `{"title":"X"}`},
		{"Карточка задачи", "GET", taskPath, ""},
		{"История задачи", "GET", taskPath + "/history", ""},
		{"Список задач", "GET", proj + "/tasks", ""},
		{"Карточка проекта", "GET", proj, ""},
		{"Изменение проекта", "PATCH", proj, `{"name":"X"}`},
		{"Участники", "GET", proj + "/members", ""},
		{"Добавление участника", "PUT", proj + "/members/" + member, `{"role":"member"}`},
		{"Исключение участника", "DELETE", proj + "/members/" + member, ""},
	} {
		t.Run("outsider/"+r.action, func(t *testing.T) {
			rec := w.do(r.method, r.path, w.outsider, r.body)
			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		})
	}

	// «Да» по тем же строкам: у кого право есть, тот проходит (порядок важен — строки меняют состав).
	newcomer := w.user(t)
	for _, r := range []struct {
		token        string
		method, path string
		body         string
		want         int
	}{
		{w.manager, "PATCH", proj, `{"notification_settings":{"daily_summary":true}}`, http.StatusOK},
		{w.admin, "PATCH", proj, `{"name":"Администратор"}`, http.StatusOK},
		{w.manager, "GET", "/api/v1/users", "", http.StatusOK},
		{w.member, "GET", proj + "/members", "", http.StatusOK},
		{w.admin, "GET", proj, "", http.StatusOK},
		{w.admin, "GET", proj + "/members", "", http.StatusOK},
		{w.manager, "PUT", proj + "/members/" + newcomer, `{"role":"member"}`, http.StatusCreated},
		{w.manager, "PUT", proj + "/members/" + newcomer, `{"role":"manager"}`, http.StatusOK},
		{w.admin, "PUT", proj + "/members/" + newcomer, `{"role":"member"}`, http.StatusOK},
		{w.admin, "DELETE", proj + "/members/" + newcomer, "", http.StatusNoContent},
		{w.manager, "PUT", proj + "/members/" + newcomer, `{"role":"member"}`, http.StatusCreated},
		{w.manager, "DELETE", proj + "/members/" + newcomer, "", http.StatusNoContent},
		{w.admin, "PATCH", proj, `{"is_archived":true}`, http.StatusOK},
		{w.admin, "PATCH", proj, `{"is_archived":false}`, http.StatusOK},
		{w.member, "POST", proj + "/tasks", `{"title":"Создавать задачи — все участники"}`, http.StatusCreated},
		{w.manager, "POST", proj + "/tasks", `{"title":"С исполнителем","assignee_id":"` + member + `"}`, http.StatusCreated},
		{w.admin, "POST", proj + "/tasks", `{"title":"Администратор","assignee_id":"` + member + `"}`, http.StatusCreated},
		{w.member, "GET", taskPath, "", http.StatusOK},
	} {
		rec := w.do(r.method, r.path, r.token, r.body)
		require.Equal(t, r.want, rec.Code, "%s %s: %s", r.method, r.path, rec.Body.String())
	}
}
