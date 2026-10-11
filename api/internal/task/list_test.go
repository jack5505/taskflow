package task_test

import (
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
		"assignee_id=me": "assignee_id", "sprint_id=x": "sprint_id", "limit=201": "limit", "limit=0": "limit", "cursor=@@@": "cursor", "include_deleted=maybe": "include_deleted"} {
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
