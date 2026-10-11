package task_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/task"
)

func (f *fixture) moveTask(t *testing.T, token, key, body string) (int, map[string]any) {
	t.Helper()
	rec, b := f.do(t, "POST", "/api/v1/tasks/"+key+"/move", token, body)
	return rec.Code, b
}

func (f *fixture) order(t *testing.T, query string) []string {
	t.Helper()
	_, p := f.list(t, f.member, query)
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.Key)
	}
	return out
}

func (f *fixture) id(t *testing.T, key string) string { return f.get(t, key).ID.String() }

// Критерий #24: порядок сохраняется после перезагрузки списка; переставлять может любой участник.
func TestReorderWithinSection(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, f.member, `{"title":"A"}`)["key"].(string)
	b := f.create(t, f.member, `{"title":"B"}`)["key"].(string)
	c := f.create(t, f.member, `{"title":"C"}`)["key"].(string)

	code, _ := f.moveTask(t, f.member, c, `{"after_task_id":"`+f.id(t, a)+`"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{a, c, b}, f.order(t, "sprint_id=null"))
	code, _ = f.moveTask(t, f.member, b, `{"after_task_id":null}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{b, a, c}, f.order(t, "sprint_id=null"), "null — в начало")
	code, _ = f.moveTask(t, f.member, b, `{"after_task_id":"`+f.id(t, c)+`"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{a, c, b}, f.order(t, "sprint_id=null"), "в конец")

	code, body := f.moveTask(t, f.member, a, `{"after_task_id":"`+f.id(t, a)+`"}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "after_task_id", field(t, body))
	code, _ = f.moveTask(t, f.outsider, a, `{"after_task_id":null}`)
	require.Equal(t, http.StatusNotFound, code)
}

// Критерий #24: исполнитель не может сменить спринт (403); перенос в активный спринт и из него —
// в журнал спринта и события.
func TestMoveBetweenSections(t *testing.T) {
	f := newFixture(t)
	active := f.sprint(t, f.p.ID, "active")
	closed := f.sprint(t, f.p.ID, "closed")
	x := f.create(t, f.member, `{"title":"X"}`)["key"].(string)
	y := f.create(t, f.manager, `{"title":"Y","sprint_id":"`+active.String()+`"}`)["key"].(string)

	code, _ := f.moveTask(t, f.member, x, `{"sprint_id":"`+active.String()+`","after_task_id":null}`)
	require.Equal(t, http.StatusForbidden, code)
	code, b := f.moveTask(t, f.manager, x, `{"sprint_id":"`+active.String()+`","after_task_id":"`+f.id(t, y)+`"}`)
	require.Equal(t, http.StatusOK, code, "%v", b)
	require.Equal(t, active.String(), b["sprint_id"])
	require.Equal(t, []string{y, x}, f.order(t, "sprint_id="+active.String()))

	code, _ = f.moveTask(t, f.manager, x, `{"sprint_id":null,"after_task_id":null}`)
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, f.get(t, x).SprintID)

	var log []string
	require.NoError(t, f.db.Raw(`SELECT l.action FROM sprint_log l JOIN tasks t ON t.id = l.task_id WHERE t.key IN (?, ?) ORDER BY l.created_at`, x, y).Scan(&log).Error)
	require.Equal(t, []string{"added", "added", "removed"}, log, "Y создана в активном, X добавлена и убрана")
	for typ, want := range map[string]int64{"sprint.task_added": 2, "sprint.task_removed": 1} {
		var n int64
		require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = ? AND payload->>'sprint_id' = ?`, typ, active.String()).Scan(&n).Error)
		require.Equal(t, want, n, typ)
	}

	code, b = f.moveTask(t, f.manager, x, `{"sprint_id":"`+closed.String()+`","after_task_id":null}`)
	require.Equal(t, http.StatusBadRequest, code, "в завершённый нельзя")
	require.Equal(t, "sprint_id", field(t, b))
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", y).Update("sprint_id", closed).Error)
	code, b = f.moveTask(t, f.manager, y, `{"sprint_id":null,"after_task_id":null}`)
	require.Equal(t, http.StatusBadRequest, code, "из завершённого нельзя")
	require.Equal(t, "sprint_id", field(t, b))

	code, b = f.moveTask(t, f.manager, x, `{"after_task_id":"`+uuid.NewString()+`"}`)
	require.Equal(t, http.StatusBadRequest, code, "задачи нет в секции")
	require.Equal(t, "after_task_id", field(t, b))
}

// Исчерпанный зазор между соседями — секция перенумеровывается, порядок верный.
func TestMoveRenumbersWhenGapExhausted(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, f.member, `{"title":"A"}`)["key"].(string)
	b := f.create(t, f.member, `{"title":"B"}`)["key"].(string)
	c := f.create(t, f.member, `{"title":"C"}`)["key"].(string)
	require.NoError(t, f.db.Exec(`UPDATE tasks SET position = 1 WHERE key = ?`, a).Error)
	require.NoError(t, f.db.Exec(`UPDATE tasks SET position = 1.0000000001 WHERE key = ?`, b).Error)
	code, _ := f.moveTask(t, f.member, c, `{"after_task_id":"`+f.id(t, a)+`"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{a, c, b}, f.order(t, "sprint_id=null"))
}
