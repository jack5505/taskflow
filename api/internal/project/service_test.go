package project_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/httpserver"
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

type fixture struct {
	db   *gorm.DB
	auth *auth.Service
	svc  *project.Service
	r    http.Handler
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	g := testutil.SharedDB(t)
	rdb := queue.NewRedis(testutil.SharedRedisAddr(t))
	t.Cleanup(func() { _ = rdb.Close() })
	cfg := &config.Config{JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: 15 * time.Minute, JWTRefreshTTL: time.Hour}
	authSvc := auth.NewService(cfg, g, rdb)
	svc := project.NewService(g).WithInvites(authSvc, inviteBaseURL)
	authH := auth.NewHandlers(authSvc, false)
	srv := httpserver.New(&config.Config{Env: "test"}, g, rdb).Mount(authH.Register).
		Mount(project.NewHandlers(svc, authH.RequireAuth()).WithInvites(authH).Register)
	return &fixture{db: g, auth: authSvc, svc: svc, r: srv.Router()}
}

func (f *fixture) user(t *testing.T, admin bool) (*user.User, string) {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), passwordHash(), "Пользователь")
	u.IsSystemAdmin = admin
	require.NoError(t, f.db.Create(u).Error)
	s, err := f.auth.Login(context.Background(), u.Email, password, "")
	require.NoError(t, err)
	return u, s.AccessToken
}

func (f *fixture) project(t *testing.T, members map[*user.User]string) *project.Project {
	t.Helper()
	p := project.New(testutil.UniqueKey(t), "Проект")
	require.NoError(t, f.db.Create(p).Error)
	for u, role := range members {
		require.NoError(t, f.db.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: role}).Error)
	}
	return p
}

func (f *fixture) do(t *testing.T, method, path, token, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
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

func field(t *testing.T, body map[string]any) string {
	t.Helper()
	fields := body["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)
	require.Len(t, fields, 1)
	return fields[0].(map[string]any)["field"].(string)
}

func keysOf(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var list []struct{ Key string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	keys := make([]string, len(list))
	for i, p := range list {
		keys[i] = p.Key
	}
	return keys
}

func outboxCount(t *testing.T, g *gorm.DB, eventType, key string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM outbox_events WHERE type = ? AND payload->>'key' = ?`, eventType, key).Scan(&n).Error)
	return n
}

func TestCreateProject(t *testing.T) {
	f := newFixture(t)
	_, admin := f.user(t, true)
	key := testutil.UniqueKey(t)

	rec, body := f.do(t, "POST", "/api/v1/projects", admin, `{"key":"`+key+`","name":" Таск-менеджер ","description":"  "}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, key, body["key"])
	require.Equal(t, "Таск-менеджер", body["name"])
	require.Nil(t, body["description"], "пустое описание — нет описания")
	require.Equal(t, "Europe/Moscow", body["timezone"])
	require.Equal(t, "10:00", body["daily_summary_time"])
	require.Equal(t, false, body["is_archived"])
	require.NotContains(t, rec.Body.String(), "task_counter")
	ns := body["notification_settings"].(map[string]any)
	require.Equal(t, true, ns["task_created"])
	require.Equal(t, false, ns["daily_summary"], "умолчания §8.6")
	require.Nil(t, body["my_role"])

	rec, body = f.do(t, "POST", "/api/v1/projects", admin, `{"key":"`+key+`","name":"Дубль"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "key", field(t, body))

	for _, bad := range []string{"T", "tm", "TOOLONGKEYXX", "T1"} {
		rec, body = f.do(t, "POST", "/api/v1/projects", admin, `{"key":"`+bad+`","name":"X"}`)
		require.Equal(t, http.StatusBadRequest, rec.Code, bad)
		require.Equal(t, apierr.CodeValidation, errCode(body))
	}
	rec, body = f.do(t, "POST", "/api/v1/projects", admin, `{"key":"`+testutil.UniqueKey(t)+`","name":"X","timezone":"Nowhere/City"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "timezone", field(t, body))

	_, member := f.user(t, false)
	rec, _ = f.do(t, "POST", "/api/v1/projects", member, `{"key":"`+testutil.UniqueKey(t)+`","name":"X"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// §3.2: ключ удалённого проекта нельзя занять новым.
func TestDeletedProjectKeyStaysTaken(t *testing.T) {
	f := newFixture(t)
	_, admin := f.user(t, true)
	p := f.project(t, nil)
	rec, _ := f.do(t, "DELETE", "/api/v1/projects/"+p.Key, admin, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec, body := f.do(t, "POST", "/api/v1/projects", admin, `{"key":"`+p.Key+`","name":"Новый"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "key", field(t, body))
}

func TestListAndGetVisibility(t *testing.T) {
	f := newFixture(t)
	me, token := f.user(t, false)
	_, admin := f.user(t, true)
	mine := f.project(t, map[*user.User]string{me: project.RoleManager})
	archived := f.project(t, map[*user.User]string{me: project.RoleMember})
	require.NoError(t, f.db.Model(archived).Update("is_archived", true).Error)
	foreign := f.project(t, nil)

	rec, _ := f.do(t, "GET", "/api/v1/projects", token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{mine.Key}, keysOf(t, rec), "только свои, без архива")
	var list []struct {
		MyRole *string `json:"my_role"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Equal(t, project.RoleManager, *list[0].MyRole)

	rec, _ = f.do(t, "GET", "/api/v1/projects?archived=true", token, "")
	require.Equal(t, []string{archived.Key}, keysOf(t, rec))

	rec, body := f.do(t, "GET", "/api/v1/projects?deleted=true", token, "")
	require.Equal(t, http.StatusForbidden, rec.Code, "корзина — только администратору")
	require.Equal(t, apierr.CodeForbidden, errCode(body))
	rec, body = f.do(t, "GET", "/api/v1/projects?archived=maybe", token, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "archived", field(t, body))

	rec, _ = f.do(t, "GET", "/api/v1/projects", admin, "")
	require.Contains(t, keysOf(t, rec), foreign.Key, "администратору — все")

	rec, _ = f.do(t, "GET", "/api/v1/projects/"+mine.Key, token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec, body = f.do(t, "GET", "/api/v1/projects/"+foreign.Key, token, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "чужой проект не виден — 404, а не 403")
	require.Equal(t, apierr.CodeNotFound, errCode(body))
	rec, body = f.do(t, "GET", "/api/v1/projects/"+foreign.Key, admin, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, body["my_role"])
}

func TestUpdateProject(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	member, uToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager, member: project.RoleMember})
	path := "/api/v1/projects/" + p.Key

	rec, body := f.do(t, "PATCH", path, mToken, `{"name":"Новое","description":"Описание","timezone":"Asia/Tashkent","daily_summary_time":"9:05","notification_settings":{"daily_summary":true,"task_created":false}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "Новое", body["name"])
	require.Equal(t, "Описание", body["description"])
	require.Equal(t, "Asia/Tashkent", body["timezone"])
	require.Equal(t, "09:05", body["daily_summary_time"])
	ns := body["notification_settings"].(map[string]any)
	require.Equal(t, true, ns["daily_summary"])
	require.Equal(t, false, ns["task_created"])
	require.Equal(t, true, ns["sprint_started"], "не переданные — без изменений")

	rec, body = f.do(t, "PATCH", path, mToken, `{"notification_settings":{"task_created":true},"description":""}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, body["description"], `description "" очищает`)
	ns = body["notification_settings"].(map[string]any)
	require.Equal(t, true, ns["task_created"])
	require.Equal(t, true, ns["daily_summary"], "прежнее изменение сохранилось")

	for req, fld := range map[string]string{
		`{"notification_settings":{"unknown":true}}`: "notification_settings",
		`{"daily_summary_time":"25:00"}`:             "daily_summary_time",
		`{"name":"   "}`:                             "name",
		`{"timezone":""}`:                            "timezone",
	} {
		rec, body = f.do(t, "PATCH", path, mToken, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, req)
		require.Equal(t, fld, field(t, body), req)
	}

	rec, _ = f.do(t, "PATCH", path, uToken, `{"name":"X"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "исполнитель не меняет проект")
	rec, _ = f.do(t, "PATCH", path, mToken, `{"is_archived":true}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "архивирует только администратор")
}

// Критерий #15: в архивном проекте изменения отклоняются 409 PROJECT_ARCHIVED.
func TestArchiveBlocksChanges(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	_, admin := f.user(t, true)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	path := "/api/v1/projects/" + p.Key

	rec, body := f.do(t, "PATCH", path, admin, `{"is_archived":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, true, body["is_archived"])
	require.EqualValues(t, 1, outboxCount(t, f.db, events.ProjectArchived, p.Key))

	for _, tc := range []struct{ token, body string }{
		{mToken, `{"name":"X"}`},
		{admin, `{"notification_settings":{"daily_summary":true}}`},
		{admin, `{"is_archived":true}`},
		{admin, `{"is_archived":false,"name":"X"}`},
		{admin, `{}`},
	} {
		rec, body = f.do(t, "PATCH", path, tc.token, tc.body)
		require.Equal(t, http.StatusConflict, rec.Code, tc.body)
		require.Equal(t, apierr.CodeProjectArchived, errCode(body))
	}
	_, err := project.NextTaskNumber(f.db, p.ID)
	require.ErrorIs(t, err, project.ErrArchived, "задачу в архивном проекте не создать")

	rec, body = f.do(t, "PATCH", path, admin, `{"is_archived":false}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, false, body["is_archived"])
	rec, _ = f.do(t, "PATCH", path, mToken, `{"name":"После архива"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.EqualValues(t, 1, outboxCount(t, f.db, events.ProjectArchived, p.Key), "разархивация события не пишет")
}

func TestDeleteAndRestore(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	_, admin := f.user(t, true)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	chat := int64(-100) - testutil.Next()
	require.NoError(t, f.db.Model(p).Update("telegram_chat_id", chat).Error)
	path := "/api/v1/projects/" + p.Key

	rec, _ := f.do(t, "DELETE", path, mToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code, "удаляет только администратор")

	rec, _ = f.do(t, "DELETE", path, admin, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.EqualValues(t, 1, outboxCount(t, f.db, events.ProjectDeleted, p.Key))
	rec, _ = f.do(t, "GET", path, mToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/projects", mToken, "")
	require.NotContains(t, keysOf(t, rec), p.Key)
	rec, _ = f.do(t, "GET", "/api/v1/projects?deleted=true", admin, "")
	require.Contains(t, keysOf(t, rec), p.Key)
	rec, _ = f.do(t, "DELETE", path, admin, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "уже удалён")

	rec, body := f.do(t, "POST", path+"/restore", admin, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, body["telegram_chat_id"], "группа отключена при удалении и не возвращается")
	rec, _ = f.do(t, "GET", path, mToken, "")
	require.Equal(t, http.StatusOK, rec.Code, "после восстановления снова виден")
	rec, _ = f.do(t, "POST", path+"/restore", admin, "")
	require.Equal(t, http.StatusOK, rec.Code, "восстановление не удалённого — без изменений")
	rec, _ = f.do(t, "POST", "/api/v1/projects/NOPE/restore", admin, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "POST", path+"/restore", mToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// Критерий #15: параллельное создание 100 задач даёт 100 разных номеров.
func TestNextTaskNumberIsAtomic(t *testing.T) {
	f := newFixture(t)
	p := f.project(t, nil)

	const n = 100
	nums := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f.db.Transaction(func(tx *gorm.DB) error {
				var err error
				nums[i], err = project.NextTaskNumber(tx, p.ID)
				return err
			})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	sort.Ints(nums)
	for i, v := range nums {
		require.Equal(t, i+1, v, "номера 1..100 без повторов и пропусков")
	}

	require.NoError(t, f.db.Delete(p).Error)
	_, err := project.NextTaskNumber(f.db, p.ID)
	require.ErrorIs(t, err, project.ErrNotFound)
}

func TestNotificationSettingsMerge(t *testing.T) {
	stored, err := project.MergeNotifications(nil, map[string]bool{project.NotifyDailySummary: true, project.NotifyTaskCreated: true})
	require.NoError(t, err)
	require.JSONEq(t, `{"daily_summary":true}`, string(stored), "хранятся только отличия от умолчаний")
	eff := project.EffectiveNotifications(stored)
	require.True(t, eff[project.NotifyDailySummary])
	require.False(t, eff[project.NotifyCommentCreated])
	require.Len(t, eff, len(project.NotificationKeys()))
	_, err = project.MergeNotifications(stored, map[string]bool{"bogus": true})
	require.Error(t, err)
	require.True(t, project.EffectiveNotifications(events.JSON("not json"))[project.NotifyTaskCreated], "битые настройки — умолчания")
}

// Ревью #80: форма настроек присылает is_archived как есть — менеджеру это не 403.
func TestManagerMaySendUnchangedIsArchived(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	rec, body := f.do(t, "PATCH", "/api/v1/projects/"+p.Key, mToken, `{"name":"Из формы","is_archived":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "Из формы", body["name"])
}

// В архивном проекте 409 раньше валидации: клиент получает одинаковый ответ.
func TestArchivedWinsOverValidation(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	require.NoError(t, f.db.Model(p).Update("is_archived", true).Error)
	rec, body := f.do(t, "PATCH", "/api/v1/projects/"+p.Key, mToken, `{"timezone":"Bad/Zone"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, apierr.CodeProjectArchived, errCode(body))
}

// Роль перепроверяется под блокировкой: понижение между Access и Update не даёт прав менеджера.
func TestUpdateRechecksRoleUnderLock(t *testing.T) {
	f := newFixture(t)
	manager, _ := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	a, err := f.svc.Access(context.Background(), manager, p.Key)
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&project.Member{}).Where("project_id = ? AND user_id = ?", p.ID, manager.ID).
		Update("role", project.RoleMember).Error)
	name := "X"
	_, err = f.svc.Update(context.Background(), a, project.UpdateInput{Name: &name})
	require.ErrorIs(t, err, project.ErrForbidden)
}

func TestRestoreRefreshesUpdatedAtAndTaskNumberDoesNot(t *testing.T) {
	f := newFixture(t)
	p := f.project(t, nil)
	var before project.Project
	require.NoError(t, f.db.First(&before, "id = ?", p.ID).Error)
	_, err := project.NextTaskNumber(f.db, p.ID)
	require.NoError(t, err)
	var after project.Project
	require.NoError(t, f.db.First(&after, "id = ?", p.ID).Error)
	require.True(t, before.UpdatedAt.Equal(after.UpdatedAt), "номер задачи не меняет updated_at проекта")

	require.NoError(t, f.svc.Delete(context.Background(), p.Key))
	time.Sleep(10 * time.Millisecond)
	restored, err := f.svc.Restore(context.Background(), p.Key)
	require.NoError(t, err)
	require.False(t, restored.DeletedAt.Valid)
	require.True(t, restored.UpdatedAt.After(after.UpdatedAt), "в ответе — свежий updated_at")
}

// §7.2: карточка проекта — активный спринт с датами, открытые задачи и задачи на пользователе.
func TestListCardCounters(t *testing.T) {
	f := newFixture(t)
	me, token := f.user(t, false)
	other, _ := f.user(t, false)
	adminUser, admin := f.user(t, true)
	withSprint := f.project(t, map[*user.User]string{me: project.RoleMember, other: project.RoleMember})
	empty := f.project(t, map[*user.User]string{me: project.RoleManager})

	sprint := func(p *project.Project, name, state string) {
		require.NoError(t, f.db.Exec(`INSERT INTO sprints (project_id, name, state, start_date, end_date)
			VALUES (?, ?, ?, '2026-10-01', '2026-10-14')`, p.ID, name, state).Error)
	}
	sprint(withSprint, "Спринт 1", "closed")
	sprint(withSprint, "Спринт 2", "active")
	sprint(withSprint, "Спринт 3", "planned")
	n := 0
	task := func(p *project.Project, status string, assignee *user.User, deleted bool) {
		n++
		var assigneeID any
		if assignee != nil {
			assigneeID = assignee.ID
		}
		var deletedAt any
		if deleted {
			deletedAt = time.Now()
		}
		require.NoError(t, f.db.Exec(`INSERT INTO tasks (project_id, number, key, title, status, reporter_id, assignee_id, position, deleted_at)
			VALUES (?, ?, ?, 'Задача', ?, ?, ?, ?, ?)`, p.ID, n, p.Key+"-"+strconv.Itoa(n), status, other.ID, assigneeID, n, deletedAt).Error)
	}
	task(withSprint, "todo", nil, false)
	task(withSprint, "in_progress", me, false)
	task(withSprint, "review", me, false)
	task(withSprint, "review", other, false)
	task(withSprint, "done", me, false)
	task(withSprint, "cancelled", me, false)
	task(withSprint, "todo", me, true)
	task(withSprint, "todo", adminUser, false) // администратор — исполнитель, но не участник

	type card struct {
		Key          string
		MyRole       *string `json:"my_role"`
		OpenTasks    int     `json:"open_tasks"`
		MyOpenTasks  int     `json:"my_open_tasks"`
		ActiveSprint *struct {
			ID        string
			Name      string
			StartDate *string `json:"start_date"`
			EndDate   *string `json:"end_date"`
		} `json:"active_sprint"`
	}
	list := func(token string) map[string]card {
		rec, _ := f.do(t, "GET", "/api/v1/projects", token, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var cards []card
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cards))
		out := map[string]card{}
		for _, c := range cards {
			out[c.Key] = c
		}
		return out
	}

	cards := list(token)
	require.Len(t, cards, 2)
	c := cards[withSprint.Key]
	require.Equal(t, 5, c.OpenTasks, "todo, in_progress, review; без done, cancelled и удалённых")
	require.Equal(t, 2, c.MyOpenTasks)
	require.NotNil(t, c.ActiveSprint)
	require.Equal(t, "Спринт 2", c.ActiveSprint.Name)
	require.NotEmpty(t, c.ActiveSprint.ID)
	require.Equal(t, "2026-10-01", *c.ActiveSprint.StartDate)
	require.Equal(t, "2026-10-14", *c.ActiveSprint.EndDate)

	e := cards[empty.Key]
	require.Nil(t, e.ActiveSprint, "нет активного спринта — null")
	require.Zero(t, e.OpenTasks)
	require.Zero(t, e.MyOpenTasks)
	require.Equal(t, project.RoleManager, *e.MyRole)

	// Администратор не участник: роли нет, задача на нём в «на мне» не считается — как в «Моих задачах».
	a := list(admin)[withSprint.Key]
	require.Equal(t, 5, a.OpenTasks)
	require.Zero(t, a.MyOpenTasks)
	require.Nil(t, a.MyRole)
}
