package task_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/project"
)

func (f *fixture) patch(t *testing.T, token, key, ifMatch, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("PATCH", "/api/v1/tasks/"+key, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	f.r.ServeHTTP(rec, r)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

// Критерий #21: два конкурентных PATCH с одной версией — второй получает 409 и видит актуальные данные.
func TestConcurrentPatchVersionConflict(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"Исходное"}`)["key"].(string)

	codes := make([]int, 2)
	bodies := make([]map[string]any, 2)
	var wg sync.WaitGroup
	for i, title := range []string{"Правка A", "Правка B"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, b := f.patch(t, f.member, key, `"1"`, `{"title":"`+title+`"}`)
			codes[i], bodies[i] = rec.Code, b
		}()
	}
	wg.Wait()
	ok, conflict := -1, -1
	for i, c := range codes {
		switch c {
		case http.StatusOK:
			ok = i
		case http.StatusConflict:
			conflict = i
		}
	}
	require.True(t, ok >= 0 && conflict >= 0, "один 200 и один 409: %v", codes)
	require.Equal(t, apierr.CodeVersionConflict, errCode(bodies[conflict]))
	current := bodies[conflict]["error"].(map[string]any)["details"].(map[string]any)["current"].(map[string]any)
	require.Equal(t, bodies[ok]["title"], current["title"], "проигравший видит чужую правку")
	require.EqualValues(t, 2, current["version"])
	require.Equal(t, []any{1.0}, bodies[conflict]["error"].(map[string]any)["details"].(map[string]any)["expected_version"])
	require.Equal(t, bodies[ok]["title"], f.get(t, key).Title, "первая правка не потеряна")

	rec, _ := f.patch(t, f.member, key, "", `{"title":"X"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "без If-Match")
	require.Contains(t, rec.Body.String(), `"field":"If-Match"`)
	for _, h := range []string{`W/"2"`, `2`, `"x"`, `"1", W/"2"`} {
		rec, b := f.patch(t, f.member, key, h, `{"title":"Плохой If-Match"}`)
		require.Equal(t, http.StatusBadRequest, rec.Code, "If-Match %s: %v", h, b)
		require.Equal(t, "If-Match", field(t, b))
	}
	rec, b := f.patch(t, f.member, key, `"1", "2"`, `{"title":"Список ETag"}`)
	require.Equal(t, http.StatusOK, rec.Code, "версия 2 есть в списке: %v", b)
	require.Equal(t, `"3"`, rec.Header().Get("ETag"))
	rec, b = f.patch(t, f.member, key, `"1", "2"`, `{"title":"Устаревший список"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "версии 3 в списке нет: %v", b)
	rec, b = f.patch(t, f.member, key, `*`, `{"title":"Без сверки"}`)
	require.Equal(t, http.StatusOK, rec.Code, "* — без сверки версии: %v", b)
	require.Equal(t, `"4"`, rec.Header().Get("ETag"))
}

func TestPatchFieldsAndHistory(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"X","description":"Текст","estimate_minutes":60,"due_date":"2026-10-31","labels":["a"]}`)["key"].(string)
	rec, b := f.patch(t, f.member, key, `"1"`, `{"title":" Новое ","priority":"critical","type":"bug","description":null,"due_date":null,"estimate_minutes":null,"labels":["b"," b "]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "Новое", b["title"])
	require.Equal(t, "critical", b["priority"])
	require.Nil(t, b["description"])
	require.Nil(t, b["due_date"])
	require.Nil(t, b["estimate_minutes"])
	require.Equal(t, []any{"b"}, b["labels"])
	require.EqualValues(t, 2, b["version"])

	var fields []string
	tk := f.get(t, key)
	require.NoError(t, f.db.Raw(`SELECT field FROM task_history WHERE task_id = ? AND field <> 'status' ORDER BY field`, tk.ID).Scan(&fields).Error)
	require.Equal(t, []string{"due_date", "estimate_minutes", "priority", "title", "type"}, fields, "описание и метки — не в истории §3.7")

	var nulls int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM task_history WHERE task_id = ? AND field IN ('estimate_minutes', 'due_date') AND new_value IS NULL`, tk.ID).Scan(&nulls).Error)
	require.EqualValues(t, 2, nulls, "очищенное поле — SQL NULL в истории, а не JSON null")

	rec, b = f.patch(t, f.member, key, `"2"`, `{"title":"Новое","labels":["b"]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.EqualValues(t, 2, b["version"], "без фактических изменений версия не растёт")

	for body, fld := range map[string]string{`{"title":null}`: "title", `{"type":"epic"}`: "type", `{"priority":null}`: "priority", `{"estimate_minutes":0}`: "estimate_minutes", `{"due_date":"завтра"}`: "due_date"} {
		rec, b = f.patch(t, f.member, key, `"2"`, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Equal(t, fld, field(t, b), body)
	}

	var n int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'task.updated' AND payload->>'key' = ?`, key).Scan(&n).Error)
	require.EqualValues(t, 1, n)
}

// §2 и §4: кто что может менять.
func TestPatchRights(t *testing.T) {
	f := newFixture(t)
	other, otherToken := f.user(t, false)
	f.join(t, other, project.RoleMember)
	sprint := f.sprint(t, f.p.ID, "planned")
	key := f.create(t, f.member, `{"title":"X","estimate_minutes":30}`)["key"].(string)
	v := func() string { return `"` + jsonInt(f.get(t, key).Version) + `"` }

	rec, _ := f.patch(t, otherToken, key, v(), `{"title":"Чужая"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "не автор и не исполнитель")
	rec, _ = f.patch(t, f.member, key, v(), `{"assignee_id":"`+other.ID.String()+`"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "назначает менеджер")
	rec, _ = f.patch(t, f.member, key, v(), `{"sprint_id":"`+sprint.String()+`"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "спринт — менеджер")

	rec, b := f.patch(t, f.manager, key, v(), `{"assignee_id":"`+other.ID.String()+`","sprint_id":"`+sprint.String()+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, other.ID.String(), b["assignee_id"])
	require.Equal(t, sprint.String(), b["sprint_id"])
	var assigned int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'task.assigned' AND payload->>'key' = ?`, key).Scan(&assigned).Error)
	require.EqualValues(t, 1, assigned)

	rec, _ = f.patch(t, otherToken, key, v(), `{"title":"Правит исполнитель"}`)
	require.Equal(t, http.StatusOK, rec.Code, "исполнитель правит свою задачу")

	require.NoError(t, f.db.Model(&struct{}{}).Exec(`UPDATE tasks SET status = 'in_progress' WHERE key = ?`, key).Error)
	rec, _ = f.patch(t, otherToken, key, v(), `{"estimate_minutes":45}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "оценку в работе меняет менеджер")
	rec, _ = f.patch(t, f.manager, key, v(), `{"estimate_minutes":45}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec, b = f.patch(t, f.manager, key, v(), `{"assignee_id":null}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "с задачи в работе исполнителя не снимают")
	require.Equal(t, "assignee_id", field(t, b))

	require.NoError(t, f.db.Exec(`UPDATE tasks SET status = 'done' WHERE key = ?`, key).Error)
	rec, b = f.patch(t, f.manager, key, v(), `{"assignee_id":"`+f.memberU.ID.String()+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "в закрытой задаче не назначают")
	require.Equal(t, "assignee_id", field(t, b))

	rec, _ = f.patch(t, f.outsider, key, v(), `{"title":"X"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.NoError(t, f.db.Model(f.p).Update("is_archived", true).Error)
	rec, b = f.patch(t, f.manager, key, v(), `{"title":"В архиве"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, apierr.CodeProjectArchived, errCode(b))
}

// Замечания ревью #21: спринты, оценка после работы, порядок ошибок исполнителя, статус в PATCH.
func TestPatchRules(t *testing.T) {
	f := newFixture(t)
	closed := f.sprint(t, f.p.ID, "closed")
	planned := f.sprint(t, f.p.ID, "planned")
	key := f.create(t, f.member, `{"title":"X","estimate_minutes":30}`)["key"].(string)
	v := func() string { return `"` + jsonInt(f.get(t, key).Version) + `"` }

	rec, b := f.patch(t, f.member, key, v(), `{"status":"done"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "статус — только через /transition")
	require.Equal(t, "status", field(t, b))

	rec, b = f.patch(t, f.manager, key, v(), `{"sprint_id":"`+closed.String()+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "в завершённый спринт не добавляют")
	require.Equal(t, "sprint_id", field(t, b))
	require.NoError(t, f.db.Exec(`UPDATE tasks SET sprint_id = ? WHERE key = ?`, closed, key).Error)
	for _, body := range []string{`{"sprint_id":null}`, `{"sprint_id":"` + planned.String() + `"}`} {
		rec, b = f.patch(t, f.manager, key, v(), body)
		require.Equal(t, http.StatusBadRequest, rec.Code, "из завершённого спринта не переносят: %s", body)
		require.Equal(t, "sprint_id", field(t, b))
	}

	inactive, _ := f.user(t, false)
	f.join(t, inactive, project.RoleMember)
	require.NoError(t, f.db.Model(inactive).Update("is_active", false).Error)
	rec, _ = f.patch(t, f.member, key, v(), `{"assignee_id":"`+inactive.ID.String()+`"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "сначала права, потом активность исполнителя")
	rec, b = f.patch(t, f.manager, key, v(), `{"assignee_id":"`+inactive.ID.String()+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "менеджеру — неактивный исполнитель")
	require.Equal(t, "assignee_id", field(t, b))

	require.NoError(t, f.db.Exec(`UPDATE tasks SET status = 'cancelled', started_at = now() WHERE key = ?`, key).Error)
	rec, _ = f.patch(t, f.member, key, v(), `{"estimate_minutes":45}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "оценку отменённой после работы задачи меняет менеджер")
	rec, _ = f.patch(t, f.manager, key, v(), `{"estimate_minutes":45}`)
	require.Equal(t, http.StatusOK, rec.Code)

	other := f.create(t, f.member, `{"title":"Не начатая","estimate_minutes":30}`)["key"].(string)
	require.NoError(t, f.db.Exec(`UPDATE tasks SET status = 'cancelled' WHERE key = ?`, other).Error)
	rec, _ = f.patch(t, f.member, other, `*`, `{"estimate_minutes":45}`)
	require.Equal(t, http.StatusOK, rec.Code, "отменённую до начала работы правит автор")
}

func TestETagOnGet(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"X"}`)["key"].(string)
	rec, _ := f.do(t, "GET", "/api/v1/tasks/"+key, f.member, "")
	require.Equal(t, `"1"`, rec.Header().Get("ETag"))
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
