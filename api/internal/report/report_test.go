package report_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/app"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/report"
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

// actor — пользователь с токеном.
type actor struct {
	u     *user.User
	token string
}

// world — два проекта A и B. В A: менеджер, исполнители member и other; в B: member (исполнитель).
// Администратор и посторонний ни в одном не участвуют.
type world struct {
	db                                     *gorm.DB
	r                                      http.Handler
	authSvc                                *auth.Service
	a, b                                   *project.Project
	admin, manager, member, other, outside actor
	n                                      int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	g := testutil.SharedDB(t)
	addr := testutil.SharedRedisAddr(t)
	rdb, jobs := queue.NewRedis(addr), queue.NewClient(addr)
	t.Cleanup(func() { _ = rdb.Close(); _ = jobs.Close() })
	cfg := &config.Config{Env: "test", BaseURL: "http://localhost", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: 15 * time.Minute, JWTRefreshTTL: time.Hour}
	w := &world{db: g, r: app.NewServer(cfg, g, rdb, jobs).Router(), authSvc: auth.NewService(cfg, g, rdb)}
	w.a = project.New(testutil.UniqueKey(t), "Проект A")
	w.b = project.New(testutil.UniqueKey(t), "Проект B")
	require.NoError(t, g.Create(w.a).Error)
	require.NoError(t, g.Create(w.b).Error)
	w.admin = w.user(t, "Админ", true)
	w.manager = w.user(t, "Менеджер", false)
	w.member = w.user(t, "Иван", false)
	w.other = w.user(t, "Пётр", false)
	w.outside = w.user(t, "Посторонний", false)
	w.join(t, w.a, w.manager, project.RoleManager)
	w.join(t, w.a, w.member, project.RoleMember)
	w.join(t, w.a, w.other, project.RoleMember)
	w.join(t, w.b, w.member, project.RoleMember)
	return w
}

func (w *world) user(t *testing.T, name string, admin bool) actor {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), passwordHash(), name)
	u.IsSystemAdmin = admin
	require.NoError(t, w.db.Create(u).Error)
	s, err := w.authSvc.Login(context.Background(), u.Email, password, "")
	require.NoError(t, err)
	return actor{u: u, token: s.AccessToken}
}

func (w *world) join(t *testing.T, p *project.Project, a actor, role string) {
	t.Helper()
	require.NoError(t, w.db.Create(&project.Member{ProjectID: p.ID, UserID: a.u.ID, Role: role}).Error)
}

// task — задача проекта напрямую в базе (даты работы в прошлом API не принял бы).
func (w *world) task(t *testing.T, p *project.Project, title string, assignee *actor, sprint *uuid.UUID, estimate *int) (uuid.UUID, string) {
	t.Helper()
	w.n++
	key := p.Key + "-" + strconv.Itoa(w.n)
	var aid *uuid.UUID
	if assignee != nil {
		aid = &assignee.u.ID
	}
	var id string
	require.NoError(t, w.db.Raw(`INSERT INTO tasks (project_id, number, key, title, reporter_id, assignee_id, sprint_id,
		estimate_minutes, position, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'in_progress') RETURNING id`,
		p.ID, w.n, key, title, w.manager.u.ID, aid, sprint, estimate, w.n).Scan(&id).Error)
	return uuid.MustParse(id), key
}

// log — запись времени с пересчётом spent_minutes, как делает сервис задач.
func (w *world) log(t *testing.T, task uuid.UUID, a actor, date string, minutes int) {
	t.Helper()
	require.NoError(t, w.db.Exec(`INSERT INTO worklogs (task_id, user_id, minutes, work_date, source)
		VALUES (?, ?, ?, ?, 'manual')`, task, a.u.ID, minutes, date).Error)
	require.NoError(t, w.db.Exec(`UPDATE tasks SET spent_minutes = spent_minutes + ? WHERE id = ?`, minutes, task).Error)
}

func (w *world) get(t *testing.T, a actor, query string) (*httptest.ResponseRecorder, *report.Report) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/reports/time?"+query, nil)
	r.Header.Set("Authorization", "Bearer "+a.token)
	w.r.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		return rec, nil
	}
	var rep report.Report
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rep))
	}
	return rec, &rep
}

func (w *world) ok(t *testing.T, a actor, query string) *report.Report {
	t.Helper()
	rec, rep := w.get(t, a, query)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rep
}

// week — неделя пн 05.10.2026 – вс 11.10.2026.
const week = "from=2026-10-05&to=2026-10-11"

func userRow(r *report.Report, id uuid.UUID) *report.UserRow {
	for i := range r.Users {
		if r.Users[i].UserID == id {
			return &r.Users[i]
		}
	}
	return nil
}

func TestByUser(t *testing.T) {
	w := newWorld(t)
	t1, _ := w.task(t, w.a, "Первая", &w.member, nil, nil)
	t2, _ := w.task(t, w.a, "Вторая", &w.other, nil, nil)
	tb, _ := w.task(t, w.b, "В проекте B", &w.member, nil, nil)
	w.log(t, t1, w.member, "2026-10-05", 90)
	w.log(t, t1, w.member, "2026-10-05", 45) // §10.4: 1h 30m + 45m = 2.25
	w.log(t, t2, w.member, "2026-10-10", 60) // суббота
	w.log(t, t2, w.other, "2026-10-06", 30)
	w.log(t, t1, w.other, "2026-10-12", 600) // вне периода
	w.log(t, tb, w.member, "2026-10-07", 120)

	r := w.ok(t, w.manager, week+"&project="+w.a.Key)
	require.Equal(t, "user", r.GroupBy)
	require.Len(t, r.Days, 7)
	require.Equal(t, report.Day{Date: "2026-10-05", Weekend: false}, r.Days[0])
	require.True(t, r.Days[5].Weekend)
	require.True(t, r.Days[6].Weekend)
	require.Len(t, r.Users, 2)
	require.Equal(t, "Иван", r.Users[0].UserName, "строки по имени")
	iv := userRow(r, w.member.u.ID)
	require.Equal(t, []int{135, 0, 0, 0, 0, 60, 0}, iv.Minutes, "проект B в отчёт по A не входит")
	require.Equal(t, 195, iv.TotalMinutes)
	require.Equal(t, "3.25", iv.Hours)
	require.Equal(t, []int{135, 30, 0, 0, 0, 60, 0}, r.DayTotals)
	require.Equal(t, 225, r.TotalMinutes)
	require.Equal(t, "3h 45m", r.Total)
	require.Equal(t, "3.75", r.Hours)
	require.Empty(t, r.Tasks)

	// Исполнитель: только свои записи (§2, §6.6), в том числе по проекту A.
	r = w.ok(t, w.member, week+"&project="+w.a.Key)
	require.Len(t, r.Users, 1)
	require.Equal(t, 195, r.TotalMinutes)
	// Без проекта — свои записи во всех своих проектах.
	r = w.ok(t, w.member, week)
	require.Len(t, r.Users, 1)
	require.Equal(t, 315, r.TotalMinutes)
	r = w.ok(t, w.member, week+"&user_id="+w.member.u.ID.String())
	require.Equal(t, 315, r.TotalMinutes)

	// Менеджер A без фильтра проекта: всё по A и ничего из B, где он не участник.
	r = w.ok(t, w.manager, week)
	require.Equal(t, 225, r.TotalMinutes)
	r = w.ok(t, w.manager, week+"&user_id="+w.other.u.ID.String())
	require.Equal(t, 30, r.TotalMinutes)

	// Администратор видит все проекты.
	r = w.ok(t, w.admin, week+"&user_id="+w.member.u.ID.String())
	require.Equal(t, 315, r.TotalMinutes)

	// Посторонний: без проекта — пусто, с проектом — 404.
	r = w.ok(t, w.outside, week)
	require.Empty(t, r.Users)
	require.Equal(t, []int{0, 0, 0, 0, 0, 0, 0}, r.DayTotals)
	rec, _ := w.get(t, w.outside, week+"&project="+w.a.Key)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// §2: «Смотреть отчёты по времени всего проекта — исполнитель: только по себе».
func TestMemberCannotSeeOthers(t *testing.T) {
	w := newWorld(t)
	other := "&user_id=" + w.other.u.ID.String()
	for _, q := range []string{week + other, week + "&project=" + w.a.Key + other} {
		rec, _ := w.get(t, w.member, q)
		require.Equal(t, http.StatusForbidden, rec.Code, q)
	}
	// Менеджер A, но исполнитель в B: другого пользователя по B не получает.
	rec, _ := w.get(t, w.manager, week+"&project="+w.b.Key)
	require.Equal(t, http.StatusNotFound, rec.Code)
	w.join(t, w.b, w.manager, project.RoleMember)
	rec, _ = w.get(t, w.manager, week+"&project="+w.b.Key+"&user_id="+w.member.u.ID.String())
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestByTask(t *testing.T) {
	w := newWorld(t)
	est := 240
	t1, k1 := w.task(t, w.a, "Первая", &w.member, nil, &est)
	t2, k2 := w.task(t, w.a, "Удалённая", nil, nil, nil)
	w.log(t, t1, w.member, "2026-10-05", 60)
	w.log(t, t1, w.other, "2026-10-06", 30)
	w.log(t, t1, w.member, "2026-09-01", 100) // до периода — только во «всего»
	w.log(t, t2, w.other, "2026-10-07", 15)
	require.NoError(t, w.db.Exec(`UPDATE tasks SET deleted_at = now() WHERE id = ?`, t2).Error)

	r := w.ok(t, w.manager, week+"&group_by=task&project="+w.a.Key)
	require.Equal(t, "task", r.GroupBy)
	require.Len(t, r.Tasks, 2)
	a := r.Tasks[0]
	require.Equal(t, k1, a.Key)
	require.Equal(t, "Первая", a.Title)
	require.Equal(t, "Иван", *a.AssigneeName)
	require.Equal(t, "in_progress", a.Status)
	require.Equal(t, 240, *a.EstimateMinutes)
	require.Equal(t, 90, a.PeriodMinutes)
	require.Equal(t, 190, a.TotalMinutes)
	require.Equal(t, "1.50", a.PeriodHours)
	require.False(t, a.Deleted)
	// §4: записи удалённой задачи остаются в отчёте с пометкой.
	d := r.Tasks[1]
	require.Equal(t, k2, d.Key)
	require.True(t, d.Deleted)
	require.Nil(t, d.AssigneeName)
	require.Equal(t, 105, r.TotalMinutes)
	require.Empty(t, r.Users)

	// Исполнитель: по задачам — только свои записи, и «всего» тоже только своё.
	r = w.ok(t, w.member, week+"&group_by=task")
	require.Len(t, r.Tasks, 1)
	require.Equal(t, 60, r.Tasks[0].PeriodMinutes)
	require.Equal(t, 160, r.Tasks[0].TotalMinutes)
	// Менеджер с фильтром по пользователю — так же.
	r = w.ok(t, w.manager, week+"&group_by=task&user_id="+w.other.u.ID.String())
	require.Len(t, r.Tasks, 2)
	require.Equal(t, 30, r.Tasks[0].TotalMinutes)

	// Менеджер A и исполнитель B без фильтра проекта: по A «всего» — всё время задачи, по B — своё.
	w.join(t, w.b, w.manager, project.RoleMember)
	tb, kb := w.task(t, w.b, "В проекте B", &w.manager, nil, nil)
	w.log(t, tb, w.manager, "2026-10-08", 20)
	w.log(t, tb, w.member, "2026-10-08", 40)
	r = w.ok(t, w.manager, week+"&group_by=task")
	require.Len(t, r.Tasks, 3)
	byKey := map[string]report.TaskRow{}
	for _, row := range r.Tasks {
		byKey[row.Key] = row
	}
	require.Equal(t, 190, byKey[k1].TotalMinutes)
	require.Equal(t, 20, byKey[kb].PeriodMinutes)
	require.Equal(t, 20, byKey[kb].TotalMinutes)

	// Удалённый проект исчезает из отчётов (§3.2), архивный — остаётся.
	require.NoError(t, w.db.Exec(`UPDATE projects SET is_archived = true WHERE id = ?`, w.a.ID).Error)
	r = w.ok(t, w.admin, week+"&group_by=task&project="+w.a.Key)
	require.Len(t, r.Tasks, 2)
	require.NoError(t, w.db.Exec(`UPDATE projects SET deleted_at = now() WHERE id = ?`, w.a.ID).Error)
	r = w.ok(t, w.admin, week+"&group_by=task&user_id="+w.other.u.ID.String())
	require.Empty(t, r.Tasks)
}

func TestSprintFilter(t *testing.T) {
	w := newWorld(t)
	sprint := func(p *project.Project) uuid.UUID {
		var id string
		require.NoError(t, w.db.Raw(`INSERT INTO sprints (project_id, name) VALUES (?, 'S') RETURNING id`, p.ID).Scan(&id).Error)
		return uuid.MustParse(id)
	}
	sa, sb := sprint(w.a), sprint(w.b)
	in, _ := w.task(t, w.a, "В спринте", &w.member, &sa, nil)
	out, _ := w.task(t, w.a, "В бэклоге", &w.member, nil, nil)
	w.log(t, in, w.member, "2026-10-05", 60)
	w.log(t, out, w.member, "2026-10-05", 30)

	r := w.ok(t, w.manager, week+"&sprint_id="+sa.String())
	require.Equal(t, 60, r.TotalMinutes)
	r = w.ok(t, w.manager, week+"&project="+w.a.Key+"&sprint_id="+sa.String())
	require.Equal(t, 60, r.TotalMinutes)
	// Спринт чужого проекта, спринт не того проекта, несуществующий — 404.
	for _, q := range []string{
		"&sprint_id=" + sb.String(),
		"&project=" + w.a.Key + "&sprint_id=" + sb.String(),
		"&sprint_id=" + uuid.NewString(),
	} {
		rec, _ := w.get(t, w.manager, week+q)
		require.Equal(t, http.StatusNotFound, rec.Code, q)
	}
}

func TestValidation(t *testing.T) {
	w := newWorld(t)
	for q, field := range map[string]string{
		"from=2026-13-01":                    "from",
		"to=10.10.2026":                      "to",
		"from=2026-10-05&to=2026-10-04":      "to",
		"from=2025-01-01&to=2026-10-04":      "to",
		"group_by=project":                   "group_by",
		"format=xlsx":                        "format",
		"user_id=1":                          "user_id",
		"sprint_id=x":                        "sprint_id",
		"project=" + strings.Repeat("A", 11): "project",
	} {
		rec, _ := w.get(t, w.manager, q)
		require.Equal(t, http.StatusBadRequest, rec.Code, q)
		require.Contains(t, rec.Body.String(), `"field":"`+field+`"`, q)
	}
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/reports/time", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	// Ровно 366 дней — можно.
	w.ok(t, w.manager, "from=2025-10-11&to=2026-10-11")
	// Одна граница — вторая на неделю от неё.
	r := w.ok(t, w.manager, "from=2026-12-01")
	require.Equal(t, "2026-12-07", r.To)
	r = w.ok(t, w.manager, "to=2025-01-07")
	require.Equal(t, "2025-01-01", r.From)
}

// Период по умолчанию — текущая неделя пн–вс по поясу пользователя (§6.6).
func TestDefaultPeriod(t *testing.T) {
	w := newWorld(t)
	svc := report.NewService(w.db, project.NewService(w.db))
	// Вс 11.10.2026 22:30 UTC — в Москве уже пн 12.10.
	svc.WithClock(func() time.Time { return time.Date(2026, 10, 11, 22, 30, 0, 0, time.UTC) })
	r, err := svc.Time(context.Background(), w.member.u, report.Query{})
	require.NoError(t, err)
	require.Equal(t, "2026-10-12", r.From)
	require.Equal(t, "2026-10-18", r.To)
	w.member.u.Timezone = "UTC"
	r, err = svc.Time(context.Background(), w.member.u, report.Query{})
	require.NoError(t, err)
	require.Equal(t, "2026-10-05", r.From)
	require.Equal(t, "2026-10-11", r.To)
	_, err = svc.Time(context.Background(), w.member.u, report.Query{GroupBy: "x"})
	require.Error(t, err)
}

func readCSV(t *testing.T, body []byte) [][]string {
	t.Helper()
	require.True(t, bytes.HasPrefix(body, []byte("\xEF\xBB\xBF")), "UTF-8 с BOM")
	require.Contains(t, string(body), "\r\n")
	r := csv.NewReader(bytes.NewReader(body[3:]))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	require.NoError(t, err)
	return rows
}

func TestCSV(t *testing.T) {
	w := newWorld(t)
	est := 90
	title := "=Отчёт; \"кавычки\"\nи перевод строки"
	t1, k1 := w.task(t, w.a, title, &w.member, nil, &est)
	w.log(t, t1, w.member, "2026-10-05", 150)
	w.log(t, t1, w.member, "2026-10-10", 15)
	require.NoError(t, w.db.Exec(`UPDATE tasks SET deleted_at = now() WHERE id = ?`, t1).Error)

	rec, _ := w.get(t, w.manager, week+"&project="+w.a.Key+"&format=csv")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="time-report-user-2026-10-05_2026-10-11.csv"`, rec.Header().Get("Content-Disposition"))
	rows := readCSV(t, rec.Body.Bytes())
	require.Equal(t, []string{"Пользователь", "05.10.2026", "06.10.2026", "07.10.2026", "08.10.2026",
		"09.10.2026", "10.10.2026", "11.10.2026", "Итого"}, rows[0])
	require.Equal(t, []string{"Иван", "2,50", "0,00", "0,00", "0,00", "0,00", "0,25", "0,00", "2,75"}, rows[1])
	require.Equal(t, []string{"Итого", "2,50", "0,00", "0,00", "0,00", "0,00", "0,25", "0,00", "2,75"}, rows[2])

	rec, _ = w.get(t, w.manager, week+"&project="+w.a.Key+"&format=csv&group_by=task")
	require.Equal(t, http.StatusOK, rec.Code)
	// Значение с ; кавычками и переводом строки — в кавычках, кавычка удвоена.
	require.Contains(t, rec.Body.String(), `"'=Отчёт; ""кавычки""`+"\r\nи перевод строки\"")
	rows = readCSV(t, rec.Body.Bytes())
	require.Equal(t, []string{"Ключ", "Название", "Исполнитель", "Статус", "Оценка, ч",
		"Списано за период, ч", "Списано всего, ч", "Пометка"}, rows[0])
	require.Equal(t, []string{k1, "'" + title, "Иван", "В работе", "1,50", "2,75", "2,75", "удалённая задача"}, rows[1])
	require.Equal(t, []string{"Итого", "", "", "", "", "2,75", "", ""}, rows[2])
}

func TestWriteCSVUnknownStatusAndEmpty(t *testing.T) {
	var buf bytes.Buffer
	name := "@Имя"
	require.NoError(t, report.WriteCSV(&buf, &report.Report{GroupBy: "task", Tasks: []report.TaskRow{{Key: "TM-1", Title: "+1", Status: "new", AssigneeName: &name}}}))
	rows := readCSV(t, buf.Bytes())
	require.Equal(t, []string{"TM-1", "'+1", "'@Имя", "new", "", "0,00", "0,00", ""}, rows[1])
	buf.Reset()
	require.NoError(t, report.WriteCSV(&buf, &report.Report{GroupBy: "user", Days: []report.Day{{Date: "bad"}}}))
	rows = readCSV(t, buf.Bytes())
	require.Equal(t, []string{"Пользователь", "bad", "Итого"}, rows[0])
	require.Error(t, report.WriteCSV(failWriter{}, &report.Report{}))
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errFail }

var errFail = errors.New("fail")
