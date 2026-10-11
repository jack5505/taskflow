package task_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

func (f *fixture) adminU(t *testing.T) *user.User {
	t.Helper()
	u, _ := f.user(t, true)
	return u
}

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

// Ревью #91: перенос на то же место — без новой версии и события.
func TestMoveNoOp(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, f.member, `{"title":"A"}`)["key"].(string)
	b := f.create(t, f.member, `{"title":"B"}`)["key"].(string)
	code, body := f.moveTask(t, f.member, b, `{"after_task_id":"`+f.id(t, a)+`"}`)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 1, body["version"], "уже после A")
	code, body = f.moveTask(t, f.member, a, `{"after_task_id":null}`)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 1, body["version"], "уже первая")
}

// Ревью #91: одновременные перестановки в одной секции — без ошибок и без одинаковых позиций,
// в том числе с перенумерацией.
func TestConcurrentReorder(t *testing.T) {
	f := newFixture(t)
	svc := task.NewService(f.db, project.NewService(f.db))
	var keys []string
	for range 6 {
		keys = append(keys, f.create(t, f.member, `{"title":"X"}`)["key"].(string))
	}
	require.NoError(t, f.db.Exec(`UPDATE tasks SET position = 1 + number * 1e-9 WHERE key IN ?`, keys).Error) // зазоры исчерпаны
	first := f.get(t, keys[0]).ID
	var wg sync.WaitGroup
	errs := make([]error, len(keys)-1)
	for i, k := range keys[1:] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = svc.Move(context.Background(), f.memberU, k, task.MoveInput{AfterTaskID: &first})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	var dup int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM (SELECT position FROM tasks WHERE key IN ? GROUP BY position HAVING count(*) > 1) d`, keys).Scan(&dup).Error)
	require.Zero(t, dup, "позиции не совпадают")
	require.Equal(t, keys[0], f.order(t, "sprint_id=null")[0])
}

// Ревью #91: удаление и восстановление задачи активного спринта — в журнале спринта.
func TestDeleteInActiveSprintLogs(t *testing.T) {
	f := newFixture(t)
	active := f.sprint(t, f.p.ID, "active")
	key := f.create(t, f.manager, `{"title":"X","sprint_id":"`+active.String()+`"}`)["key"].(string)
	svc := task.NewService(f.db, project.NewService(f.db))
	require.NoError(t, svc.Delete(context.Background(), f.managerU, key))
	_, err := svc.Restore(context.Background(), f.adminU(t), key)
	require.NoError(t, err)
	var log []string
	require.NoError(t, f.db.Raw(`SELECT l.action FROM sprint_log l JOIN tasks t ON t.id = l.task_id WHERE t.key = ? ORDER BY l.created_at`, key).Scan(&log).Error)
	require.Equal(t, []string{"added", "removed", "added"}, log)
	var by []string
	require.NoError(t, f.db.Raw(`SELECT l.user_id::text FROM sprint_log l JOIN tasks t ON t.id = l.task_id WHERE t.key = ? ORDER BY l.created_at LIMIT 1`, key).Scan(&by).Error)
	require.Equal(t, []string{f.managerU.ID.String()}, by, "кто действовал — явно, не из контекста")
}
