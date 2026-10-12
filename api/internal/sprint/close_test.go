package sprint_test

import (
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/task"
)

func (f *fixture) setStatus(t *testing.T, key, status string) {
	t.Helper()
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", key).Update("status", status).Error)
}

func (f *fixture) sprintOf(t *testing.T, key string) any {
	t.Helper()
	_, b := f.do(t, "GET", "/api/v1/tasks/"+key, f.manager, "")
	return b["sprint_id"]
}

// Сценарий приёмки §10.4: завершение спринта с тремя незакрытыми задачами переносит их в выбранный спринт.
func TestCloseWithThreeOpenTasks(t *testing.T) {
	f := newFixture(t)
	s := f.sprint(t, `{`+dates+`}`)
	next := f.sprint(t, `{"name":"Следующий"}`)
	done := f.task(t, s, "60")
	cancelled := f.task(t, s, "")
	open := []string{f.task(t, s, "30"), f.task(t, s, ""), f.task(t, s, "15")}
	rec, _ := f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code)
	f.setStatus(t, done, "done")
	f.setStatus(t, cancelled, "cancelled")
	f.setStatus(t, open[1], "in_progress")
	f.setStatus(t, open[2], "review")

	rec, _ = f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.member, `{"move_open_to":"backlog"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, b := f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.manager, `{"move_open_to":"`+next+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	sp := b["sprint"].(map[string]any)
	require.Equal(t, "closed", sp["state"])
	require.NotNil(t, sp["closed_at"])
	sum := b["summary"].(map[string]any)
	require.EqualValues(t, 1, sum["done"])
	require.EqualValues(t, 4, sum["total"], "готово 1 из 4: отменённая не считается")
	require.EqualValues(t, 105, sum["estimate_minutes"], "оценки готовой и незакрытых")
	require.EqualValues(t, 3, sum["moved"])
	require.ElementsMatch(t, []any{open[0], open[1], open[2]}, sum["moved_keys"])

	for _, k := range open {
		require.Equal(t, next, f.sprintOf(t, k), "незакрытая перенесена в выбранный спринт")
	}
	for _, k := range []string{done, cancelled} {
		require.Equal(t, s, f.sprintOf(t, k), "done и cancelled остаются в завершённом")
	}
	var ev int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'sprint.closed' AND payload->>'sprint_id' = ?`, s).Scan(&ev).Error)
	require.EqualValues(t, 1, ev)

	rec, b = f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.manager, `{"move_open_to":"backlog"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "закрытый не закрыть повторно")
	require.Equal(t, "not_active", reason(b))
	rec, _ = f.do(t, "POST", "/api/v1/tasks/"+open[0]+"/move", f.manager, `{"sprint_id":"`+s+`","after_task_id":null}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "в закрытый задачи не добавляются")
}

func TestCloseToBacklogAndValidation(t *testing.T) {
	f := newFixture(t)
	s := f.sprint(t, `{`+dates+`}`)
	k := f.task(t, s, "")
	rec, _ := f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.manager, `{"move_open_to":"backlog"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "запланированный не завершить")
	rec, _ = f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code)

	other := f.sprint(t, `{}`)
	require.NoError(t, f.db.Exec(`UPDATE sprints SET state = 'closed' WHERE id = ?`, other).Error)
	for body, wantField := range map[string]string{
		`{"move_open_to":"` + other + `"}`:                        "move_open_to",
		`{"move_open_to":"` + s + `"}`:                            "move_open_to",
		`{"move_open_to":"00000000-0000-4000-8000-000000000000"}`: "move_open_to",
		`{"move_open_to":"куда-нибудь"}`:                          "move_open_to",
		`{}`: "move_open_to",
	} {
		rec, _ = f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.manager, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Contains(t, rec.Body.String(), `"field":"`+wantField+`"`, body)
	}
	rec, b := f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.manager, `{"move_open_to":"backlog"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.EqualValues(t, 1, b["summary"].(map[string]any)["moved"])
	require.Nil(t, f.sprintOf(t, k), "в бэклог")
}

// Ревью #98: «Backlog» без учёта регистра; удалённая незакрытая задача уходит вместе с остальными;
// для не активного спринта — 409, даже с плохой целью.
func TestCloseReviewFixes(t *testing.T) {
	f := newFixture(t)
	s := f.sprint(t, `{`+dates+`}`)
	live := f.task(t, s, "")
	gone := f.task(t, s, "")
	rec, b := f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.manager, `{"move_open_to":"00000000-0000-4000-8000-000000000000"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "не активный — 409 раньше ошибки цели")
	require.Equal(t, "not_active", reason(b))
	rec, _ = f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = f.do(t, "DELETE", "/api/v1/tasks/"+gone, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec, _ = f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.manager, `{"move_open_to":"Backlog"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, f.sprintOf(t, live))
	var sprintID *string
	require.NoError(t, f.db.Raw(`SELECT sprint_id::text FROM tasks WHERE key = ?`, gone).Scan(&sprintID).Error)
	require.Nil(t, sprintID, "удалённая незакрытая тоже ушла из закрытого спринта")
}

// #160: переоткрытие задачи из завершённого спринта одновременно с закрытием активного. Задача
// не должна остаться незакрытой в закрытом спринте: либо её перенесёт закрытие, либо переоткрытие
// после закрытия положит её в бэклог.
func TestCloseAndReopenRace(t *testing.T) {
	f := newFixture(t)
	for i := range 20 {
		old := f.sprint(t, `{`+dates+`}`)
		d := f.task(t, old, "")
		rec, _ := f.do(t, "POST", "/api/v1/sprints/"+old+"/start", f.manager, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		f.setStatus(t, d, "done")
		rec, _ = f.do(t, "POST", "/api/v1/sprints/"+old+"/close", f.manager, `{"move_open_to":"backlog"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		cur := f.sprint(t, `{`+dates+`}`)
		f.task(t, cur, "")
		rec, _ = f.do(t, "POST", "/api/v1/sprints/"+cur+"/start", f.manager, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		got := concurrently(t, f,
			[3]string{"POST", "/api/v1/sprints/" + cur + "/close", `{"move_open_to":"backlog"}`},
			[3]string{"POST", "/api/v1/tasks/" + d + "/transition", `{"to":"in_progress"}`})
		require.Equal(t, []string{"200", "200"}, got, "попытка %d", i)
		require.Nil(t, f.sprintOf(t, d), "попытка %d: незакрытая задача не осталась в закрытом спринте", i)
	}
}

// #160: перенос задачи из спринта одновременно со стартом спринта. Журнал спринта (§5.3) должен
// отражать итог: задача в объёме на старте и затем убрана — есть запись «убрано»; не попала
// в объём — записи нет.
func TestStartAndMoveOutRace(t *testing.T) {
	f := newFixture(t)
	for i := range 20 {
		s := f.sprint(t, `{`+dates+`}`)
		k := f.task(t, s, "")
		f.task(t, s, "") // спринт не пустой и без k
		got := concurrently(t, f,
			[3]string{"POST", "/api/v1/sprints/" + s + "/start", ""},
			[3]string{"POST", "/api/v1/tasks/" + k + "/move", `{"sprint_id":null,"after_task_id":null}`})
		require.Equal(t, []string{"200", "200"}, got, "попытка %d", i)
		var inScope, removed int64
		require.NoError(t, f.db.Raw(`SELECT count(*) FROM sprint_start_scope ss JOIN tasks t ON t.id = ss.task_id
			WHERE ss.sprint_id = ? AND t.key = ?`, s, k).Scan(&inScope).Error)
		require.NoError(t, f.db.Raw(`SELECT count(*) FROM sprint_log l JOIN tasks t ON t.id = l.task_id
			WHERE l.sprint_id = ? AND t.key = ? AND l.action = 'removed'`, s, k).Scan(&removed).Error)
		require.Equal(t, inScope, removed, "попытка %d: убрана после старта ⇔ запись в журнале", i)
		rec, _ := f.do(t, "POST", "/api/v1/sprints/"+s+"/close", f.manager, `{"move_open_to":"backlog"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
}

// concurrently выполняет запросы менеджера одновременно: горутины ждут общего старта, чтобы
// запросы действительно пересеклись. Ответ — код, а для неуспешных — код и тело (для сообщения).
func concurrently(t *testing.T, f *fixture, reqs ...[3]string) []string {
	t.Helper()
	got := make([]string, len(reqs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, r := range reqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec, _ := f.do(t, r[0], r[1], f.manager, r[2])
			got[i] = strconv.Itoa(rec.Code)
			if rec.Code != http.StatusOK {
				got[i] += " " + rec.Body.String()
			}
		}()
	}
	close(start)
	wg.Wait()
	return got
}
