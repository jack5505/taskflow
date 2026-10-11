package task_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

type listPage struct {
	Items []struct {
		Key        string
		Status     string
		AssigneeID *string `json:"assignee_id"`
		Overdue    bool
	}
	NextCursor string `json:"next_cursor"`
}

func (f *fixture) list(t *testing.T, token, query string) (int, listPage) {
	t.Helper()
	rec, _ := f.do(t, "GET", "/api/v1/projects/"+f.p.Key+"/tasks?"+query, token, "")
	var p listPage
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return rec.Code, p
}

func keys(p listPage) []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.Key)
	}
	sort.Strings(out)
	return out
}

func sorted(k ...string) []string { sort.Strings(k); return k }

func TestListFilters(t *testing.T) {
	f := newFixture(t)
	sprint := f.sprint(t, f.p.ID, "planned")
	k := func(m map[string]any) string { return m["key"].(string) }
	bug := k(f.create(t, f.manager, `{"title":"Падает экспорт CSV","type":"bug","priority":"high","labels":["export"],"assignee_id":"`+f.memberU.ID.String()+`"}`))
	feat := k(f.create(t, f.manager, `{"title":"Новый отчёт","type":"feature","sprint_id":"`+sprint.String()+`"}`))
	free := k(f.create(t, f.member, `{"title":"100% покрытие_тестами"}`))
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", feat).Update("status", "in_progress").Error)

	for query, want := range map[string][]string{
		"type=bug":                                  {bug},
		"type=bug,feature":                          sorted(bug, feat),
		"type=bug&type=feature":                     sorted(bug, feat),
		"priority=high":                             {bug},
		"status=in_progress":                        {feat},
		"assignee_id=" + f.memberU.ID.String():      {bug},
		"assignee_id=null":                          sorted(feat, free),
		"assignee_id=null," + f.memberU.ID.String(): sorted(bug, feat, free),
		"sprint_id=" + sprint.String():              {feat},
		"sprint_id=null":                            sorted(bug, free),
		"label=export":                              {bug},
		"q=экспорт":                                 {bug},
		"q=ЭКСПОРТ":                                 {bug},
		"q=" + bug:                                  {bug},
		"q=100%25":                                  {free},
		"q=e_t":                                     {},
		"q=" + f.p.Key + "-":                        sorted(bug, feat, free),
	} {
		code, p := f.list(t, f.member, query)
		require.Equal(t, http.StatusOK, code, query)
		require.Equal(t, want, keys(p), query)
	}

	for query, field := range map[string]string{"status=open": "status", "type=epic": "type", "priority=urgent": "priority",
		"assignee_id=me": "assignee_id", "sprint_id=x": "sprint_id", "limit=201": "limit", "limit=x": "limit", "cursor=@@@": "cursor", "include_deleted=maybe": "include_deleted"} {
		rec, b := f.do(t, "GET", "/api/v1/projects/"+f.p.Key+"/tasks?"+query, f.member, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, query)
		require.Equal(t, field, b["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)[0].(map[string]any)["field"], query)
	}
	rec, _ := f.do(t, "GET", "/api/v1/projects/"+f.p.Key+"/tasks", f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// Критерий #22: курсор устойчив при вставке новых задач.
func TestListCursorStableOnInsert(t *testing.T) {
	f := newFixture(t)
	var all []string
	for range 5 {
		all = append(all, f.create(t, f.member, `{"title":"X"}`)["key"].(string))
	}
	seen := map[string]int{}
	code, p := f.list(t, f.member, "limit=2")
	require.Equal(t, http.StatusOK, code)
	for _, it := range p.Items {
		seen[it.Key]++
	}
	all = append(all, f.create(t, f.member, `{"title":"Вставлена между страницами"}`)["key"].(string))
	for p.NextCursor != "" {
		code, p = f.list(t, f.member, "limit=2&cursor="+p.NextCursor)
		require.Equal(t, http.StatusOK, code)
		for _, it := range p.Items {
			seen[it.Key]++
		}
	}
	require.Len(t, seen, len(all), "новая задача попала в выдачу, ничего не потерялось")
	for k, n := range seen {
		require.Equal(t, 1, n, "%s без повторов", k)
	}
}

func TestListDeletedAndOverdue(t *testing.T) {
	f := newFixture(t)
	gone := f.create(t, f.member, `{"title":"Удалённая"}`)["key"].(string)
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", gone).Update("deleted_at", time.Now()).Error)
	loc, _ := time.LoadLocation(f.p.Timezone)
	yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format(time.DateOnly)
	today := time.Now().In(loc).Format(time.DateOnly)
	late := f.create(t, f.member, `{"title":"Просрочена","due_date":"`+yesterday+`"}`)["key"].(string)
	onTime := f.create(t, f.member, `{"title":"Срок сегодня","due_date":"`+today+`"}`)["key"].(string)
	closedLate := f.create(t, f.member, `{"title":"Закрыта","due_date":"`+yesterday+`"}`)["key"].(string)
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", closedLate).Update("status", "done").Error)

	_, p := f.list(t, f.member, "")
	require.NotContains(t, keys(p), gone)
	overdue := map[string]bool{}
	for _, it := range p.Items {
		overdue[it.Key] = it.Overdue
	}
	require.True(t, overdue[late])
	require.False(t, overdue[onTime], "срок сегодня — ещё не просрочен")
	require.False(t, overdue[closedLate], "закрытая не просрочена")

	code, _ := f.list(t, f.member, "include_deleted=true")
	require.Equal(t, http.StatusForbidden, code)
	code, p = f.list(t, f.admin, "include_deleted=true")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, keys(p), gone)
}

func TestMyTasks(t *testing.T) {
	f := newFixture(t)
	loc, _ := time.LoadLocation(f.p.Timezone)
	day := func(d int) string { return time.Now().In(loc).AddDate(0, 0, d).Format(time.DateOnly) }
	mk := func(body string) string {
		return f.create(t, f.manager, body)["key"].(string)
	}
	me := `"assignee_id":"` + f.memberU.ID.String() + `"`
	later := mk(`{"title":"Позже",` + me + `,"due_date":"` + day(5) + `"}`)
	late := mk(`{"title":"Просрочена",` + me + `,"due_date":"` + day(-2) + `"}`)
	noDue := mk(`{"title":"Без срока",` + me + `}`)
	done := mk(`{"title":"Готова",` + me + `}`)
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", done).Update("status", "done").Error)
	mk(`{"title":"Чужая","assignee_id":"` + f.managerU.ID.String() + `"}`)

	other := project.New("Z"+f.p.Key[1:], "Второй")
	require.NoError(t, f.db.Create(other).Error)
	require.NoError(t, f.db.Create(&project.Member{ProjectID: other.ID, UserID: f.memberU.ID, Role: project.RoleMember}).Error)
	require.NoError(t, f.db.Create(&project.Member{ProjectID: other.ID, UserID: f.managerU.ID, Role: project.RoleManager}).Error)
	rec, b := f.do(t, "POST", "/api/v1/projects/"+other.Key+"/tasks", f.manager, `{"title":"Из другого проекта",`+me+`,"due_date":"`+day(1)+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	otherKey := b["key"].(string)

	rec, _ = f.do(t, "GET", "/api/v1/me/tasks", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var raw []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	got := []string{}
	for _, m := range raw {
		got = append(got, m["key"].(string))
	}
	require.Equal(t, []string{late, otherKey, later, noDue}, got, "по сроку, просроченные сверху, без срока — в конце, закрытые и чужие не входят")
	require.Equal(t, true, raw[0]["overdue"])
	require.Equal(t, other.Key, raw[1]["project_key"])
}

// Ревью #87: позиции упорядочены только внутри секции — список идёт по секциям (спринт, затем
// бэклог), курсор проходит все задачи ровно по разу и при вставке в уже пройденную секцию.
func TestListOrderAcrossSections(t *testing.T) {
	f := newFixture(t)
	sprint := f.sprint(t, f.p.ID, "planned")
	var backlog, inSprint []string
	for range 3 {
		backlog = append(backlog, f.create(t, f.member, `{"title":"Бэклог"}`)["key"].(string))
		inSprint = append(inSprint, f.create(t, f.manager, `{"title":"Спринт","sprint_id":"`+sprint.String()+`"}`)["key"].(string))
	}
	_, p := f.list(t, f.member, "")
	got := []string{}
	for _, it := range p.Items {
		got = append(got, it.Key)
	}
	require.Equal(t, append(append([]string{}, inSprint...), backlog...), got, "сначала спринт, потом бэклог, внутри — ручной порядок")

	seen := map[string]int{}
	code, p := f.list(t, f.member, "limit=2")
	require.Equal(t, http.StatusOK, code)
	for _, it := range p.Items {
		seen[it.Key]++
	}
	late := f.create(t, f.manager, `{"title":"В пройденную секцию","sprint_id":"`+sprint.String()+`"}`)["key"].(string)
	for p.NextCursor != "" {
		_, p = f.list(t, f.member, "limit=2&cursor="+p.NextCursor)
		for _, it := range p.Items {
			seen[it.Key]++
		}
	}
	for _, k := range append(inSprint, backlog...) {
		require.Equal(t, 1, seen[k], k)
	}
	require.LessOrEqual(t, seen[late], 1, "вставленная не повторяется")
}

func TestOverdueUsesServiceClock(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"X","due_date":"2026-10-31"}`)["key"].(string)
	a, err := project.NewService(f.db).Access(context.Background(), f.memberU, f.p.Key)
	require.NoError(t, err)
	for now, want := range map[time.Time]bool{
		time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC):  false, // 23:00 по Москве — ещё 31-е
		time.Date(2026, 10, 31, 21, 30, 0, 0, time.UTC): true,  // 00:30 1-го по Москве
	} {
		svc := task.NewService(f.db, project.NewService(f.db)).WithClock(func() time.Time { return now })
		page, err := svc.List(context.Background(), a, task.ListFilter{Q: key})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, want, page.Items[0].Overdue, now)
	}
}

func TestMyTasksSkipsArchivedAndSortsByNumber(t *testing.T) {
	f := newFixture(t)
	me := `"assignee_id":"` + f.memberU.ID.String() + `"`
	var keys []string
	for range 10 {
		keys = append(keys, f.create(t, f.manager, `{"title":"Без срока",`+me+`}`)["key"].(string))
	}
	rec, _ := f.do(t, "GET", "/api/v1/me/tasks", f.member, "")
	var raw []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	got := []string{}
	for _, m := range raw {
		got = append(got, m["key"].(string))
	}
	require.Equal(t, keys, got, "TM-9 раньше TM-10")

	require.NoError(t, f.db.Model(f.p).Update("is_archived", true).Error)
	rec, _ = f.do(t, "GET", "/api/v1/me/tasks", f.member, "")
	require.JSONEq(t, `[]`, rec.Body.String(), "задачи архивного проекта не показываются")
}
