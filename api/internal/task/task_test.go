package task_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/app"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/task"
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
	db                               *gorm.DB
	r                                http.Handler
	auth                             *auth.Service
	p                                *project.Project
	admin, manager, member, outsider string
	managerU, memberU                *user.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	g := testutil.SharedDB(t)
	addr := testutil.SharedRedisAddr(t)
	rdb, jobs := queue.NewRedis(addr), queue.NewClient(addr)
	t.Cleanup(func() { _ = rdb.Close(); _ = jobs.Close() })
	cfg := &config.Config{Env: "test", BaseURL: "http://localhost", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: 15 * time.Minute, JWTRefreshTTL: time.Hour}
	f := &fixture{db: g, r: app.NewServer(cfg, g, rdb, jobs).Router(), auth: auth.NewService(cfg, g, rdb)}
	_, f.admin = f.user(t, true)
	f.managerU, f.manager = f.user(t, false)
	f.memberU, f.member = f.user(t, false)
	_, f.outsider = f.user(t, false)
	f.p = project.New(testutil.UniqueKey(t), "Проект")
	require.NoError(t, g.Create(f.p).Error)
	f.join(t, f.managerU, project.RoleManager)
	f.join(t, f.memberU, project.RoleMember)
	return f
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

func (f *fixture) join(t *testing.T, u *user.User, role string) {
	t.Helper()
	require.NoError(t, f.db.Create(&project.Member{ProjectID: f.p.ID, UserID: u.ID, Role: role}).Error)
}

func (f *fixture) sprint(t *testing.T, projectID uuid.UUID, state string) uuid.UUID {
	t.Helper()
	var id string
	require.NoError(t, f.db.Raw(`INSERT INTO sprints (project_id, name, state) VALUES (?, 'Спринт', ?) RETURNING id::text`, projectID, state).Scan(&id).Error)
	return uuid.MustParse(id)
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

func (f *fixture) create(t *testing.T, token, body string) map[string]any {
	t.Helper()
	rec, m := f.do(t, "POST", "/api/v1/projects/"+f.p.Key+"/tasks", token, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return m
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
	require.NotEmpty(t, fields)
	return fields[0].(map[string]any)["field"].(string)
}

// Критерий #19: создание пишет ключ вида TM-42 и запись в историю.
func TestCreateTask(t *testing.T) {
	f := newFixture(t)
	first := f.create(t, f.member, `{"title":" Настроить экспорт ","labels":[" ui ","ui","backend"],"estimate_minutes":90,"due_date":"2026-10-31","type":"bug","priority":"high"}`)
	require.Equal(t, f.p.Key+"-1", first["key"])
	require.EqualValues(t, 1, first["number"])
	require.Equal(t, "Настроить экспорт", first["title"])
	require.Equal(t, "todo", first["status"])
	require.Equal(t, "bug", first["type"])
	require.Equal(t, "high", first["priority"])
	require.Equal(t, f.memberU.ID.String(), first["reporter_id"])
	require.Nil(t, first["assignee_id"])
	require.Nil(t, first["sprint_id"])
	require.Equal(t, []any{"ui", "backend"}, first["labels"], "пробелы обрезаны, повтор убран")
	require.Equal(t, "2026-10-31", first["due_date"])
	require.EqualValues(t, 1, first["version"])
	require.EqualValues(t, 0, first["spent_minutes"])

	second := f.create(t, f.member, `{"title":"Вторая"}`)
	require.Equal(t, f.p.Key+"-2", second["key"])
	require.Equal(t, "task", second["type"], "умолчания §3.3")
	require.Equal(t, "medium", second["priority"])
	require.Greater(t, second["position"].(float64), first["position"].(float64), "новая — в конец бэклога")

	rec, _ := f.do(t, "GET", "/api/v1/tasks/"+f.p.Key+"-1/history", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var hist []struct {
		Field    string
		OldValue any    `json:"old_value"`
		NewValue any    `json:"new_value"`
		UserID   string `json:"user_id"`
		Source   string
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &hist))
	require.Len(t, hist, 1)
	require.Equal(t, "status", hist[0].Field)
	require.Nil(t, hist[0].OldValue)
	require.Equal(t, "todo", hist[0].NewValue)
	require.Equal(t, f.memberU.ID.String(), hist[0].UserID)
	require.Equal(t, "web", hist[0].Source)

	var n int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = ? AND payload->>'key' = ?`, events.TaskCreated, f.p.Key+"-1").Scan(&n).Error)
	require.EqualValues(t, 1, n, "task.created в outbox")
}

// Критерий #19: исполнитель и спринт при создании — только менеджер.
func TestAssigneeAndSprintAtCreationAreManagerOnly(t *testing.T) {
	f := newFixture(t)
	sprint := f.sprint(t, f.p.ID, "planned")
	tasks := "/api/v1/projects/" + f.p.Key + "/tasks"

	for _, body := range []string{
		`{"title":"X","assignee_id":"` + f.memberU.ID.String() + `"}`,
		`{"title":"X","sprint_id":"` + sprint.String() + `"}`,
	} {
		rec, b := f.do(t, "POST", tasks, f.member, body)
		require.Equal(t, http.StatusForbidden, rec.Code, body)
		require.Equal(t, apierr.CodeForbidden, errCode(b))
	}

	m := f.create(t, f.manager, `{"title":"От менеджера","assignee_id":"`+f.memberU.ID.String()+`","sprint_id":"`+sprint.String()+`"}`)
	require.Equal(t, f.memberU.ID.String(), m["assignee_id"])
	require.Equal(t, sprint.String(), m["sprint_id"])
	a := f.create(t, f.admin, `{"title":"От администратора","sprint_id":"`+sprint.String()+`"}`)
	require.Greater(t, a["position"].(float64), m["position"].(float64), "в конец своей секции — спринта")

	rec, _ := f.do(t, "GET", "/api/v1/tasks/"+m["key"].(string)+"/history", f.member, "")
	require.Contains(t, rec.Body.String(), `"field":"assignee_id"`)
	require.Contains(t, rec.Body.String(), `"field":"sprint_id"`)

	other := project.New(testutil.UniqueKey(t), "Другой")
	require.NoError(t, f.db.Create(other).Error)
	inactive, _ := f.user(t, false)
	f.join(t, inactive, project.RoleMember)
	require.NoError(t, f.db.Model(inactive).Update("is_active", false).Error)
	for body, fld := range map[string]string{
		`{"title":"X","assignee_id":"` + inactive.ID.String() + `"}`:                    "assignee_id",
		`{"title":"X","assignee_id":"` + uuid.NewString() + `"}`:                        "assignee_id",
		`{"title":"X","sprint_id":"` + f.sprint(t, f.p.ID, "closed").String() + `"}`:    "sprint_id",
		`{"title":"X","sprint_id":"` + f.sprint(t, other.ID, "planned").String() + `"}`: "sprint_id",
	} {
		rec, b := f.do(t, "POST", tasks, f.manager, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Equal(t, fld, field(t, b), body)
	}
}

func TestCreateTaskValidation(t *testing.T) {
	f := newFixture(t)
	tasks := "/api/v1/projects/" + f.p.Key + "/tasks"
	for body, fld := range map[string]string{
		`{"title":"   "}`: "title",
		`{"title":"` + strings.Repeat("я", 201) + `"}`:                           "title",
		`{"title":"X","type":"epic"}`:                                            "type",
		`{"title":"X","priority":"urgent"}`:                                      "priority",
		`{"title":"X","estimate_minutes":0}`:                                     "estimate_minutes",
		`{"title":"X","estimate_minutes":60001}`:                                 "estimate_minutes",
		`{"title":"X","due_date":"31.10.2026"}`:                                  "due_date",
		`{"title":"X","labels":["` + strings.Repeat("a", 31) + `"]}`:             "labels",
		`{"title":"X","labels":["1","2","3","4","5","6","7","8","9","10","11"]}`: "labels",
		`{"title":"X","labels":[" "]}`:                                           "labels",
	} {
		rec, b := f.do(t, "POST", tasks, f.member, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Equal(t, fld, field(t, b), body)
	}

	rec, _ := f.do(t, "POST", tasks, f.outsider, `{"title":"X"}`)
	require.Equal(t, http.StatusNotFound, rec.Code, "не участник не видит проект")
	require.NoError(t, f.db.Model(f.p).Update("is_archived", true).Error)
	rec, b := f.do(t, "POST", tasks, f.member, `{"title":"X"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, apierr.CodeProjectArchived, errCode(b))
}

func TestGetTaskVisibility(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"X"}`)["key"].(string)

	rec, b := f.do(t, "GET", "/api/v1/tasks/"+key, f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, key, b["key"])
	for _, path := range []string{"/api/v1/tasks/" + key, "/api/v1/tasks/" + key + "/history"} {
		rec, _ = f.do(t, "GET", path, f.outsider, "")
		require.Equal(t, http.StatusNotFound, rec.Code, path)
	}
	for _, bad := range []string{"tm-1", "TM-0", "TM", f.p.Key + "-999"} {
		rec, _ = f.do(t, "GET", "/api/v1/tasks/"+bad, f.admin, "")
		require.Equal(t, http.StatusNotFound, rec.Code, bad)
	}

	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", key).Update("deleted_at", time.Now()).Error)
	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key, f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "удалённую не видит участник")
	rec, b = f.do(t, "GET", "/api/v1/tasks/"+key, f.admin, "")
	require.Equal(t, http.StatusOK, rec.Code, "администратор видит корзину")
	require.NotNil(t, b["deleted_at"])
}

// §3.7: история не редактируется и не удаляется.
func TestHistoryIsImmutable(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"X"}`)["key"].(string)
	var id string
	require.NoError(t, f.db.Raw(`SELECT h.id::text FROM task_history h JOIN tasks t ON t.id = h.task_id WHERE t.key = ?`, key).Scan(&id).Error)
	require.ErrorContains(t, f.db.Exec(`UPDATE task_history SET field = 'x' WHERE id = ?`, id).Error, "не изменяется")
	require.ErrorContains(t, f.db.Exec(`DELETE FROM task_history WHERE id = ?`, id).Error, "не изменяется")
}

// Параллельные создания из веба и бота: разные номера и ключи (§3.2).
func TestConcurrentCreatesGetDistinctKeys(t *testing.T) {
	f := newFixture(t)
	const n = 20
	keys := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, b := f.do(t, "POST", "/api/v1/projects/"+f.p.Key+"/tasks", f.member, `{"title":"Параллельно"}`)
			keys[i], _ = b["key"].(string)
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, k := range keys {
		require.NotEmpty(t, k)
		require.False(t, seen[k], "ключ %s выдан дважды", k)
		seen[k] = true
	}
}

// Ревью #84: метки нормализуются раньше лимитов — HTTP и бот принимают одно и то же.
func TestLabelsNormalizedBeforeLimits(t *testing.T) {
	f := newFixture(t)
	dup := `"x",`
	m := f.create(t, f.member, `{"title":"X","labels":[`+strings.Repeat(dup, 11)+`"  `+strings.Repeat("я", 30)+`  "]}`)
	require.Equal(t, []any{"x", strings.Repeat("я", 30)}, m["labels"])
}

// Ревью #84: права администратора перепроверяются в транзакции.
func TestAdminRightsRecheckedInTransaction(t *testing.T) {
	f := newFixture(t)
	admin, _ := f.user(t, true)
	svc := task.NewService(f.db, project.NewService(f.db))
	a, err := project.NewService(f.db).Access(context.Background(), admin, f.p.Key)
	require.NoError(t, err)
	require.NoError(t, f.db.Model(admin).Update("is_system_admin", false).Error)
	_, err = svc.Create(context.Background(), a, admin, task.CreateInput{Title: "X"})
	require.ErrorIs(t, err, project.ErrNotFound, "уже не администратор и не участник")
}
