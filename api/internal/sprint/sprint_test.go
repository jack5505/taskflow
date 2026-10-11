package sprint_test

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
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/sprint"
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
	db                        *gorm.DB
	r                         http.Handler
	auth                      *auth.Service
	p                         *project.Project
	manager, member, outsider string
	managerU                  *user.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	g := testutil.SharedDB(t)
	addr := testutil.SharedRedisAddr(t)
	rdb, jobs := queue.NewRedis(addr), queue.NewClient(addr)
	t.Cleanup(func() { _ = rdb.Close(); _ = jobs.Close() })
	cfg := &config.Config{Env: "test", BaseURL: "http://localhost", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: 15 * time.Minute, JWTRefreshTTL: time.Hour}
	f := &fixture{db: g, r: app.NewServer(cfg, g, rdb, jobs).Router(), auth: auth.NewService(cfg, g, rdb)}
	f.p = project.New(testutil.UniqueKey(t), "Проект")
	require.NoError(t, g.Create(f.p).Error)
	f.managerU, f.manager = f.user(t, project.RoleManager)
	_, f.member = f.user(t, project.RoleMember)
	_, f.outsider = f.user(t, "")
	return f
}

func (f *fixture) user(t *testing.T, role string) (*user.User, string) {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), passwordHash(), "Пользователь")
	require.NoError(t, f.db.Create(u).Error)
	if role != "" {
		require.NoError(t, f.db.Create(&project.Member{ProjectID: f.p.ID, UserID: u.ID, Role: role}).Error)
	}
	s, err := f.auth.Login(context.Background(), u.Email, password, "")
	require.NoError(t, err)
	return u, s.AccessToken
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

func (f *fixture) sprint(t *testing.T, body string) string {
	t.Helper()
	rec, b := f.do(t, "POST", "/api/v1/projects/"+f.p.Key+"/sprints", f.manager, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return b["id"].(string)
}

func (f *fixture) task(t *testing.T, sprintID string, estimate string) string {
	t.Helper()
	body := `{"title":"Задача","sprint_id":"` + sprintID + `"`
	if estimate != "" {
		body += `,"estimate_minutes":` + estimate
	}
	rec, b := f.do(t, "POST", "/api/v1/projects/"+f.p.Key+"/tasks", f.manager, body+`}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return b["key"].(string)
}

func code(b map[string]any) string {
	if e, ok := b["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func reason(b map[string]any) any {
	return b["error"].(map[string]any)["details"].(map[string]any)["reason"]
}

const dates = `"start_date":"2026-10-13","end_date":"2026-10-24"`

func TestCreateAndUpdateSprint(t *testing.T) {
	f := newFixture(t)
	base := "/api/v1/projects/" + f.p.Key + "/sprints"
	rec, b := f.do(t, "POST", base, f.manager, `{}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "Спринт 1", b["name"])
	require.Equal(t, "planned", b["state"])
	rec, b = f.do(t, "POST", base, f.manager, `{"goal":"Экспорт",`+dates+`}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "Спринт 2", b["name"])
	require.Equal(t, "2026-10-13", b["start_date"])
	id := b["id"].(string)

	rec, _ = f.do(t, "POST", base, f.member, `{}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "спринты создаёт менеджер")
	rec, _ = f.do(t, "POST", base, f.outsider, `{}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	for body, fld := range map[string]string{
		`{"start_date":"2026-10-13","end_date":"2026-10-13"}`: "end_date",
		`{"start_date":"2026-10-01","end_date":"2026-11-13"}`: "end_date",
		`{"start_date":"13.10.2026"}`:                         "start_date",
		`{"name":"   "}`:                                      "name",
	} {
		rec, _ = f.do(t, "POST", base, f.manager, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Contains(t, rec.Body.String(), `"field":"`+fld+`"`, body)
	}

	rec, b = f.do(t, "PATCH", "/api/v1/sprints/"+id, f.manager, `{"name":"Релиз","goal":null,"end_date":"2026-10-20"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "Релиз", b["name"])
	require.Nil(t, b["goal"])
	require.Equal(t, "2026-10-20", b["end_date"])
	rec, _ = f.do(t, "PATCH", "/api/v1/sprints/"+id, f.member, `{"name":"X"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/sprints/"+id, f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/sprints/"+id, f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	require.NoError(t, f.db.Exec(`UPDATE sprints SET state = 'closed' WHERE id = ?`, id).Error)
	rec, b = f.do(t, "PATCH", "/api/v1/sprints/"+id, f.manager, `{"name":"После закрытия"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, apierr.CodeInvalidTransition, code(b))
}

// §5.2: условия запуска и объём на старте.
func TestStartSprint(t *testing.T) {
	f := newFixture(t)
	noDates := f.sprint(t, `{}`)
	f.task(t, noDates, "")
	rec, b := f.do(t, "POST", "/api/v1/sprints/"+noDates+"/start", f.manager, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "dates", reason(b))

	empty := f.sprint(t, `{`+dates+`}`)
	rec, b = f.do(t, "POST", "/api/v1/sprints/"+empty+"/start", f.manager, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "empty", reason(b))

	s := f.sprint(t, `{`+dates+`}`)
	f.task(t, s, "60")
	f.task(t, s, "")
	f.task(t, s, "90")
	rec, _ = f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.member, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, b = f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "active", b["sprint"].(map[string]any)["state"])
	require.Equal(t, map[string]any{"tasks": 3.0, "estimate_minutes": 150.0, "without_estimate": 1.0}, b["scope"])
	var scope int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM sprint_start_scope WHERE sprint_id = ?`, s).Scan(&scope).Error)
	require.EqualValues(t, 3, scope)
	var ev int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'sprint.started' AND payload->>'sprint_id' = ?`, s).Scan(&ev).Error)
	require.EqualValues(t, 1, ev)

	other := f.sprint(t, `{`+dates+`}`)
	f.task(t, other, "")
	rec, b = f.do(t, "POST", "/api/v1/sprints/"+other+"/start", f.manager, "")
	require.Equal(t, http.StatusConflict, rec.Code, "второй активный")
	require.Equal(t, apierr.CodeSprintAlreadyActive, code(b))
	rec, b = f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusConflict, rec.Code, "уже активный")
	require.Equal(t, "not_planned", reason(b))
	rec, b = f.do(t, "DELETE", "/api/v1/sprints/"+s, f.manager, "")
	require.Equal(t, http.StatusConflict, rec.Code, "активный не удалить")
	require.Equal(t, "not_planned", reason(b))
}

// Критерий #28: два одновременных запуска не дают двух активных спринтов.
func TestConcurrentStart(t *testing.T) {
	f := newFixture(t)
	svc := sprint.NewService(f.db, project.NewService(f.db))
	ids := []string{f.sprint(t, `{`+dates+`}`), f.sprint(t, `{`+dates+`}`)}
	for _, id := range ids {
		f.task(t, id, "30")
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, errs[i] = svc.Start(context.Background(), f.managerU, uuid.MustParse(id))
		}()
	}
	close(start)
	wg.Wait()
	failed := 0
	for _, err := range errs {
		if err != nil {
			require.Equal(t, apierr.CodeSprintAlreadyActive, apierr.From(err).Code, err.Error())
			failed++
		}
	}
	require.Equal(t, 1, failed)
	var active int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM sprints WHERE project_id = ? AND state = 'active'`, f.p.ID).Scan(&active).Error)
	require.EqualValues(t, 1, active)
}

// §5.5: удалить можно запланированный, задачи — в бэклог.
func TestDeletePlannedSprint(t *testing.T) {
	f := newFixture(t)
	s := f.sprint(t, `{}`)
	key := f.task(t, s, "")
	rec, _ := f.do(t, "DELETE", "/api/v1/sprints/"+s, f.member, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, _ = f.do(t, "DELETE", "/api/v1/sprints/"+s, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec, b := f.do(t, "GET", "/api/v1/tasks/"+key, f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, b["sprint_id"], "задача вернулась в бэклог")
	var hist int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM task_history h JOIN tasks t ON t.id = h.task_id WHERE t.key = ? AND h.field = 'sprint_id' AND h.new_value IS NULL`, key).Scan(&hist).Error)
	require.EqualValues(t, 1, hist)
	rec, _ = f.do(t, "GET", "/api/v1/sprints/"+s, f.manager, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListOrderCountsAndOverdue(t *testing.T) {
	f := newFixture(t)
	closed := f.sprint(t, `{"name":"Старый"}`)
	planned := f.sprint(t, `{"name":"Следующий",`+dates+`}`)
	active := f.sprint(t, `{"name":"Текущий",`+dates+`}`)
	f.task(t, active, "45")
	f.task(t, active, "")
	rec, _ := f.do(t, "POST", "/api/v1/sprints/"+active+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, f.db.Exec(`UPDATE sprints SET state = 'closed', closed_at = now() WHERE id = ?`, closed).Error)

	a, err := project.NewService(f.db).Access(context.Background(), f.managerU, f.p.Key)
	require.NoError(t, err)
	svc := sprint.NewService(f.db, project.NewService(f.db))
	for now, want := range map[time.Time]bool{
		time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC): false,
		time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC): true,
	} {
		list, err := svc.WithClock(func() time.Time { return now }).List(context.Background(), a)
		require.NoError(t, err)
		require.Equal(t, []string{active, planned, closed}, []string{list[0].ID.String(), list[1].ID.String(), list[2].ID.String()})
		require.Equal(t, 2, list[0].TasksCount)
		require.Equal(t, 45, list[0].EstimateMinutes)
		require.Equal(t, want, list[0].Overdue, now)
	}
}

// Ревью #96: правка задачи спринта одновременно с его запуском или удалением — без deadlock.
func TestSprintOpsVsTaskEditsNoDeadlock(t *testing.T) {
	f := newFixture(t)
	svc := sprint.NewService(f.db, project.NewService(f.db))
	tasks := task.NewService(f.db, project.NewService(f.db))
	for i := range 10 {
		s := f.sprint(t, `{`+dates+`}`)
		key := f.task(t, s, "30")
		v := f.taskVersion(t, key)
		var wg sync.WaitGroup
		var editErr, opErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			title := "Правка во время операции"
			_, editErr = tasks.Update(context.Background(), f.managerU, key, []int{v}, task.UpdateInput{Title: task.Opt[string]{Set: true, Value: &title}})
		}()
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				_, _, opErr = svc.Start(context.Background(), f.managerU, uuid.MustParse(s))
			} else {
				opErr = svc.Delete(context.Background(), f.managerU, uuid.MustParse(s))
			}
		}()
		wg.Wait()
		if editErr != nil {
			require.Equal(t, apierr.CodeVersionConflict, apierr.From(editErr).Code, editErr.Error())
		}
		require.NoError(t, opErr)
		if i%2 == 0 {
			// освободить «активный» слот проекта для следующей итерации
			require.NoError(t, f.db.Exec(`UPDATE sprints SET state = 'closed' WHERE id = ?`, s).Error)
		}
	}
}

func (f *fixture) taskVersion(t *testing.T, key string) int {
	t.Helper()
	var v int
	require.NoError(t, f.db.Raw(`SELECT version FROM tasks WHERE key = ?`, key).Scan(&v).Error)
	return v
}

// Ревью #96: «Спринт N» не повторяется после удаления; перенос задач удалённого спринта —
// с событиями task.updated; права при создании перепроверяются под блокировкой.
func TestSprintReviewFixes(t *testing.T) {
	f := newFixture(t)
	first := f.sprint(t, `{}`)
	f.sprint(t, `{}`)
	key := f.task(t, first, "")
	rec, _ := f.do(t, "DELETE", "/api/v1/sprints/"+first, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	_, b := f.do(t, "POST", "/api/v1/projects/"+f.p.Key+"/sprints", f.manager, `{}`)
	require.Equal(t, "Спринт 3", b["name"], "не «Спринт 2» повторно")
	var ev int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'task.updated' AND payload->>'key' = ?`, key).Scan(&ev).Error)
	require.EqualValues(t, 1, ev)

	a, err := project.NewService(f.db).Access(context.Background(), f.managerU, f.p.Key)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec(`UPDATE project_members SET role = 'member' WHERE user_id = ?`, f.managerU.ID).Error)
	_, err = sprint.NewService(f.db, project.NewService(f.db)).Create(context.Background(), a, sprint.Input{})
	require.ErrorIs(t, err, sprint.ErrForbidden)
}
