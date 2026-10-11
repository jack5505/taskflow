package task_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// assigned — задача, где исполнитель — f.member; created_at сдвинут на 30 дней назад, чтобы
// прошлые даты работы были допустимы.
func (f *fixture) assigned(t *testing.T) string {
	t.Helper()
	m := f.create(t, f.manager, `{"title":"Задача","assignee_id":"`+f.memberU.ID.String()+`"}`)
	key := m["key"].(string)
	require.NoError(t, f.db.Exec(`UPDATE tasks SET created_at = now() - interval '30 days' WHERE key = ?`, key).Error)
	return key
}

func (f *fixture) logTime(t *testing.T, key, token, body string, want int) map[string]any {
	t.Helper()
	rec, m := f.do(t, "POST", "/api/v1/tasks/"+key+"/worklogs", token, body)
	require.Equal(t, want, rec.Code, rec.Body.String())
	return m
}

func (f *fixture) spent(t *testing.T, key string) (spent, version int) {
	t.Helper()
	require.NoError(t, f.db.Raw(`SELECT spent_minutes, version FROM tasks WHERE key = ?`, key).Row().Scan(&spent, &version))
	return spent, version
}

func today() string { return time.Now().In(time.FixedZone("MSK", 3*3600)).Format(time.DateOnly) }

func daysAgo(n int) string {
	return time.Now().In(time.FixedZone("MSK", 3*3600)).AddDate(0, 0, -n).Format(time.DateOnly)
}

// Сценарий приёмки #32: 1h 30m + 45m = 2h 15m / 2.25; version задачи не меняется.
func TestWorklogAcceptance(t *testing.T) {
	f := newFixture(t)
	key := f.assigned(t)
	_, v0 := f.spent(t, key)

	first := f.logTime(t, key, f.member, `{"duration":"1h 30m","comment":"  Разбор логов  "}`, http.StatusCreated)
	require.EqualValues(t, 90, first["minutes"])
	require.Equal(t, "1h 30m", first["duration"])
	require.Equal(t, "1.50", first["hours"])
	require.Equal(t, today(), first["work_date"], "по умолчанию — сегодня по поясу пользователя (Europe/Moscow)")
	require.Equal(t, "Разбор логов", first["comment"])
	require.Equal(t, "manual", first["source"])
	require.Equal(t, f.memberU.ID.String(), first["user_id"])
	require.Equal(t, "Пользователь", first["user_name"])
	f.logTime(t, key, f.member, `{"duration":"45m","work_date":"`+daysAgo(1)+`"}`, http.StatusCreated)

	spent, v := f.spent(t, key)
	require.Equal(t, 135, spent)
	require.Equal(t, v0, v, "запись времени не перевыпускает задачу версией")

	rec, m := f.do(t, "GET", "/api/v1/tasks/"+key, f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.EqualValues(t, 135, m["spent_minutes"])

	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/worklogs", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var list []task.WorklogView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2)
	require.Equal(t, "1h 30m", list[0].Duration, "новые сверху по дате работы")
	require.Equal(t, "45m", list[1].Duration)
	total := list[0].Minutes + list[1].Minutes
	require.Equal(t, 135, total)

	var n int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = ? AND payload->>'key' = ?`, events.WorklogCreated, key).Scan(&n).Error)
	require.EqualValues(t, 2, n)
	var payload string
	require.NoError(t, f.db.Raw(`SELECT payload::text FROM outbox_events WHERE type = ? AND payload->>'key' = ? ORDER BY created_at DESC LIMIT 1`,
		events.WorklogCreated, key).Scan(&payload).Error)
	require.Contains(t, payload, `"spent_minutes": 135`)

	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/worklogs", f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// §6.1: строка не по формату и значения вне 1m–24h — 400 INVALID_DURATION.
func TestWorklogInvalidDuration(t *testing.T) {
	f := newFixture(t)
	key := f.assigned(t)
	for _, body := range []string{`{}`, `{"duration":""}`, `{"duration":"30m 2h"}`, `{"duration":"1.5h"}`,
		`{"duration":"0m"}`, `{"duration":"25h"}`, `{"duration":"4d"}`} {
		_, m := f.do(t, "POST", "/api/v1/tasks/"+key+"/worklogs", f.member, body)
		require.Equal(t, apierr.CodeInvalidDuration, errCode(m), body)
		require.Equal(t, "duration", field(t, m))
	}
	f.logTime(t, key, f.member, `{"duration":"3d"}`, http.StatusCreated) // 3d = 24h — предел одной записи

	rec, m := f.do(t, "POST", "/api/v1/tasks/"+key+"/worklogs", f.member, `{"duration":"1h","work_date":"`+time.Now().AddDate(0, 0, 2).Format(time.DateOnly)+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "work_date", field(t, m), "не позже сегодня")
	_, m = f.do(t, "POST", "/api/v1/tasks/"+key+"/worklogs", f.member, `{"duration":"1h","work_date":"`+daysAgo(40)+`"}`)
	require.Equal(t, "work_date", field(t, m), "не раньше создания задачи")
	_, m = f.do(t, "POST", "/api/v1/tasks/"+key+"/worklogs", f.member, `{"duration":"1h","work_date":"11.10.2026"}`)
	require.Equal(t, "work_date", field(t, m))
	long := fmt.Sprintf(`{"duration":"1h","work_date":"%s","comment":"%0501d"}`, daysAgo(1), 0)
	_, m = f.do(t, "POST", "/api/v1/tasks/"+key+"/worklogs", f.member, long)
	require.Equal(t, "comment", field(t, m))
	_, m = f.do(t, "POST", "/api/v1/tasks/"+key+"/worklogs", f.member, `[]`)
	require.Equal(t, apierr.CodeInvalidJSON, errCode(m))
}

// §2: исполнитель списывает только на свои задачи; чужие записи правит и удаляет менеджер (с аудитом §3.8).
func TestWorklogRights(t *testing.T) {
	f := newFixture(t)
	key := f.assigned(t)
	free := f.create(t, f.member, `{"title":"Без исполнителя, автор — исполнитель-участник"}`)["key"].(string)

	_, m := f.do(t, "POST", "/api/v1/tasks/"+free+"/worklogs", f.member, `{"duration":"1h"}`)
	require.Equal(t, apierr.CodeForbidden, errCode(m), "автор, но не исполнитель — 403")
	rec, _ := f.do(t, "POST", "/api/v1/tasks/"+key+"/worklogs", f.outsider, `{"duration":"1h"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "POST", "/api/v1/tasks/"+f.p.Key+"-999/worklogs", f.member, `{"duration":"1h"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "POST", "/api/v1/tasks/bad/worklogs", f.member, `{"duration":"1h"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	f.logTime(t, free, f.manager, `{"duration":"30m"}`, http.StatusCreated)
	f.logTime(t, free, f.admin, `{"duration":"15m"}`, http.StatusCreated)

	mine := f.logTime(t, key, f.member, `{"duration":"1h"}`, http.StatusCreated)["id"].(string)
	theirs := f.logTime(t, key, f.manager, `{"duration":"2h"}`, http.StatusCreated)["id"].(string)

	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+theirs, f.member, `{"duration":"1h"}`)
	require.Equal(t, apierr.CodeForbidden, errCode(m))
	_, m = f.do(t, "DELETE", "/api/v1/worklogs/"+theirs, f.member, "")
	require.Equal(t, apierr.CodeForbidden, errCode(m))
	for _, id := range []string{mine, "not-uuid", "00000000-0000-0000-0000-000000000000"} {
		rec, _ = f.do(t, "PATCH", "/api/v1/worklogs/"+id, f.outsider, `{"duration":"1h"}`)
		require.Equal(t, http.StatusNotFound, rec.Code, id)
	}

	// Своя запись: без аудита.
	rec, m = f.do(t, "PATCH", "/api/v1/worklogs/"+mine, f.member, `{"duration":"1h 15m","comment":"уточнил"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "1h 15m", m["duration"])
	require.Equal(t, "уточнил", m["comment"])
	rec, m = f.do(t, "PATCH", "/api/v1/worklogs/"+mine, f.member, `{"comment":null}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, m["comment"])
	rec, _ = f.do(t, "PATCH", "/api/v1/worklogs/"+mine, f.member, `{}`)
	require.Equal(t, http.StatusOK, rec.Code, "без изменений")
	spent, _ := f.spent(t, key)
	require.Equal(t, 75+120, spent)

	// Чужая запись менеджером — с аудитом.
	rec, m = f.do(t, "PATCH", "/api/v1/worklogs/"+mine, f.manager, `{"duration":"2h","work_date":"`+daysAgo(2)+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, daysAgo(2), m["work_date"])
	require.Equal(t, f.memberU.ID.String(), m["user_id"], "владелец записи не меняется")
	rec, _ = f.do(t, "DELETE", "/api/v1/worklogs/"+theirs, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code, "свою — без аудита")
	rec, _ = f.do(t, "DELETE", "/api/v1/worklogs/"+mine, f.admin, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	spent, _ = f.spent(t, key)
	require.Equal(t, 0, spent)

	var actions []string
	require.NoError(t, f.db.Raw(`SELECT action FROM audit_logs WHERE entity_id = ? ORDER BY created_at`, mine).Scan(&actions).Error)
	require.Equal(t, []string{"worklog.updated", "worklog.deleted"}, actions)
	var n int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM audit_logs WHERE entity_id = ?`, theirs).Scan(&n).Error)
	require.Zero(t, n)
	for typ, want := range map[string]int64{events.WorklogUpdated: 3, events.WorklogDeleted: 2} {
		require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = ? AND payload->>'key' = ?`, typ, key).Scan(&n).Error)
		require.Equal(t, want, n, typ)
	}
	rec, _ = f.do(t, "DELETE", "/api/v1/worklogs/"+mine, f.manager, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// §6.5: автор правит свою запись 7 дней после work_date, менеджер — без срока.
func TestWorklogEditWindow(t *testing.T) {
	f := newFixture(t)
	key := f.assigned(t)
	old := f.logTime(t, key, f.member, `{"duration":"1h","work_date":"`+daysAgo(8)+`"}`, http.StatusCreated)["id"].(string)
	edge := f.logTime(t, key, f.member, `{"duration":"1h","work_date":"`+daysAgo(7)+`"}`, http.StatusCreated)["id"].(string)

	_, m := f.do(t, "PATCH", "/api/v1/worklogs/"+old, f.member, `{"duration":"2h"}`)
	require.Equal(t, apierr.CodeForbidden, errCode(m))
	_, m = f.do(t, "DELETE", "/api/v1/worklogs/"+old, f.member, "")
	require.Equal(t, apierr.CodeForbidden, errCode(m))
	rec, _ := f.do(t, "PATCH", "/api/v1/worklogs/"+edge, f.member, `{"duration":"2h"}`)
	require.Equal(t, http.StatusOK, rec.Code, "седьмой день — ещё можно")
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+edge, f.member, `{"work_date":"`+daysAgo(9)+`"}`)
	require.Equal(t, "work_date", field(t, m), "автор не уводит запись за 7 дней")
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+edge, f.member, `{"work_date":null}`)
	require.Equal(t, "work_date", field(t, m))
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+edge, f.member, `{"duration":null}`)
	require.Equal(t, apierr.CodeInvalidDuration, errCode(m))
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+edge, f.member, `{"duration":"x"}`)
	require.Equal(t, apierr.CodeInvalidDuration, errCode(m))
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+edge, f.member, fmt.Sprintf(`{"comment":"%0501d"}`, 0))
	require.Equal(t, "comment", field(t, m))
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+edge, f.member, `{"duration":1}`)
	require.Equal(t, apierr.CodeValidation, errCode(m))

	rec, _ = f.do(t, "PATCH", "/api/v1/worklogs/"+old, f.manager, `{"work_date":"`+daysAgo(20)+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec, _ = f.do(t, "DELETE", "/api/v1/worklogs/"+old, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
}

// §6.3: не больше 24 часов на пользователя за дату по всем задачам — 409 DAILY_LIMIT_EXCEEDED.
func TestWorklogDailyLimit(t *testing.T) {
	f := newFixture(t)
	a, b := f.assigned(t), f.assigned(t)
	day := daysAgo(1)
	f.logTime(t, a, f.member, `{"duration":"2d","work_date":"`+day+`"}`, http.StatusCreated)
	w := f.logTime(t, b, f.member, `{"duration":"7h","work_date":"`+day+`"}`, http.StatusCreated)["id"].(string)

	m := f.logTime(t, b, f.member, `{"duration":"1h 1m","work_date":"`+day+`"}`, http.StatusConflict)
	require.Equal(t, apierr.CodeDailyLimitExceeded, errCode(m))
	details := m["error"].(map[string]any)["details"].(map[string]any)
	require.EqualValues(t, 60, details["available_minutes"])
	require.EqualValues(t, 1380, details["used_minutes"])
	f.logTime(t, a, f.member, `{"duration":"1h"}`, http.StatusCreated) // другой день — свой лимит
	f.logTime(t, a, f.manager, `{"duration":"5h","work_date":"`+day+`"}`, http.StatusCreated)

	// Правка не считает прежние минуты записи, но не превышает лимит.
	rec, _ := f.do(t, "PATCH", "/api/v1/worklogs/"+w, f.member, `{"duration":"8h"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+w, f.manager, `{"duration":"8h 1m"}`)
	require.Equal(t, apierr.CodeDailyLimitExceeded, errCode(m), "лимит владельца записи, даже когда правит менеджер")
	f.logTime(t, b, f.member, `{"duration":"1m","work_date":"`+day+`"}`, http.StatusConflict)
	spentA, _ := f.spent(t, a)
	spentB, _ := f.spent(t, b)
	require.Equal(t, 960+60+300, spentA)
	require.Equal(t, 480, spentB)
}

// Две записи одного пользователя одновременно не обходят лимит: 10 запросов по 5h на одну дату
// с разных задач — проходят ровно 4 (20h), остальные 409.
func TestWorklogDailyLimitConcurrent(t *testing.T) {
	f := newFixture(t)
	keys := []string{f.assigned(t), f.assigned(t), f.assigned(t)}
	day := daysAgo(1)
	const n = 10
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec, _ := f.do(t, "POST", "/api/v1/tasks/"+keys[i%len(keys)]+"/worklogs", f.member, `{"duration":"5h","work_date":"`+day+`"}`)
			codes[i] = rec.Code
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, c := range codes {
		require.Contains(t, []int{http.StatusCreated, http.StatusConflict}, c)
		if c == http.StatusCreated {
			ok++
		}
	}
	require.Equal(t, 4, ok, "%v", codes)
	var sum int
	require.NoError(t, f.db.Raw(`SELECT COALESCE(sum(minutes), 0) FROM worklogs WHERE user_id = ? AND work_date = ?`, f.memberU.ID, day).Scan(&sum).Error)
	require.Equal(t, 1200, sum)
	total := 0
	for _, k := range keys {
		s, _ := f.spent(t, k)
		total += s
	}
	require.Equal(t, 1200, total, "spent_minutes задач сходится с записями")
}

// §6.5: задача закрытого спринта — записи заморожены; §4: на отменённую не списывают; §3.2: архив.
func TestWorklogFrozen(t *testing.T) {
	f := newFixture(t)
	key := f.assigned(t)
	id := f.logTime(t, key, f.member, `{"duration":"1h"}`, http.StatusCreated)["id"].(string)

	sprint := f.sprint(t, f.p.ID, "active")
	require.NoError(t, f.db.Exec(`UPDATE tasks SET sprint_id = ? WHERE key = ?`, sprint, key).Error)
	f.logTime(t, key, f.member, `{"duration":"1h"}`, http.StatusCreated) // активный спринт — можно
	require.NoError(t, f.db.Exec(`UPDATE sprints SET state = 'closed', closed_at = now() WHERE id = ?`, sprint).Error)
	m := f.logTime(t, key, f.manager, `{"duration":"1h"}`, http.StatusConflict)
	require.Equal(t, apierr.CodeInvalidTransition, errCode(m))
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+id, f.manager, `{"duration":"2h"}`)
	require.Equal(t, apierr.CodeInvalidTransition, errCode(m))
	_, m = f.do(t, "DELETE", "/api/v1/worklogs/"+id, f.member, "")
	require.Equal(t, apierr.CodeInvalidTransition, errCode(m))
	_, m = f.do(t, "DELETE", "/api/v1/worklogs/"+id, f.outsider, "")
	require.Equal(t, apierr.CodeNotFound, errCode(m), "404 раньше 409")

	other := f.assigned(t)
	oid := f.logTime(t, other, f.member, `{"duration":"1h"}`, http.StatusCreated)["id"].(string)
	rec, _ := f.do(t, "POST", "/api/v1/tasks/"+other+"/transition", f.manager, `{"to":"cancelled","comment":"Не нужна"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	m = f.logTime(t, other, f.manager, `{"duration":"1h"}`, http.StatusConflict)
	require.Equal(t, apierr.CodeInvalidTransition, errCode(m))
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+oid, f.manager, `{"duration":"24h"}`)
	require.Equal(t, apierr.CodeInvalidTransition, errCode(m), "правкой на отменённую задачу тоже не списывают")
	_, m = f.do(t, "POST", "/api/v1/tasks/"+other+"/worklogs", f.outsider, `{"duration":"1h"}`)
	require.Equal(t, apierr.CodeNotFound, errCode(m))

	fresh := f.assigned(t)
	require.NoError(t, f.db.Model(&project.Project{}).Where("id = ?", f.p.ID).Update("is_archived", true).Error)
	_, m = f.do(t, "POST", "/api/v1/tasks/"+fresh+"/worklogs", f.member, `{"duration":"1h"}`)
	require.Equal(t, apierr.CodeProjectArchived, errCode(m))
	_, m = f.do(t, "DELETE", "/api/v1/worklogs/"+oid, f.member, "")
	require.Equal(t, apierr.CodeProjectArchived, errCode(m))
	_, m = f.do(t, "PATCH", "/api/v1/worklogs/"+oid, f.member, `{"duration":"2h"}`)
	require.Equal(t, apierr.CodeProjectArchived, errCode(m))
	require.NoError(t, f.db.Model(&project.Project{}).Where("id = ?", f.p.ID).Update("is_archived", false).Error)
	rec, _ = f.do(t, "DELETE", "/api/v1/worklogs/"+oid, f.member, "")
	require.Equal(t, http.StatusNoContent, rec.Code, "запись отменённой задачи удалить можно — запрещено только списывать")
}

// §4: записи удалённой задачи остаются; видит их администратор, изменять нельзя.
func TestWorklogsOfDeletedTask(t *testing.T) {
	f := newFixture(t)
	key := f.assigned(t)
	id := f.logTime(t, key, f.member, `{"duration":"2h"}`, http.StatusCreated)["id"].(string)
	rec, _ := f.do(t, "DELETE", "/api/v1/tasks/"+key, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	var n int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM worklogs WHERE id = ?`, id).Scan(&n).Error)
	require.EqualValues(t, 1, n)
	spent, _ := f.spent(t, key)
	require.Equal(t, 120, spent)

	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/worklogs", f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/worklogs", f.admin, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), id)
	for _, r := range [][2]string{{"PATCH", `{"duration":"1h"}`}, {"DELETE", ""}} {
		rec, _ = f.do(t, r[0], "/api/v1/worklogs/"+id, f.manager, r[1])
		require.Equal(t, http.StatusNotFound, rec.Code, r[0])
	}
	f.logTime(t, key, f.member, `{"duration":"1h"}`, http.StatusNotFound)
	// Лимит считает и записи удалённой задачи (§6.3: по всем задачам).
	f.logTime(t, f.assigned(t), f.member, `{"duration":"22h 1m"}`, http.StatusConflict)
}

// «Сегодня» и дата работы — по поясу пользователя; источник telegram — для бота.
func TestWorklogUserTimezone(t *testing.T) {
	f := newFixture(t)
	key := f.assigned(t)
	require.NoError(t, f.db.Exec(`UPDATE tasks SET created_at = '2026-01-01' WHERE key = ?`, key).Error)
	now := time.Date(2026, 10, 10, 20, 30, 0, 0, time.UTC) // в Ташкенте (+5) уже 11 октября
	svc := task.NewService(f.db, project.NewService(f.db)).WithClock(func() time.Time { return now })
	ctx := context.Background()

	u := *f.memberU
	require.NoError(t, f.db.Exec(`UPDATE users SET timezone = 'Asia/Tashkent' WHERE id = ?`, u.ID).Error)
	v, err := svc.AddWorklog(ctx, &u, key, task.WorklogInput{Duration: "1h"})
	require.NoError(t, err)
	require.Equal(t, "2026-10-11", v.WorkDate.Format(time.DateOnly))
	tomorrow := "2026-10-12"
	_, err = svc.AddWorklog(ctx, &u, key, task.WorklogInput{Duration: "1h", WorkDate: &tomorrow})
	require.ErrorIs(t, err, apierr.BadRequest(apierr.CodeValidation, ""))

	require.NoError(t, f.db.Exec(`UPDATE users SET timezone = 'UTC' WHERE id = ?`, u.ID).Error)
	v, err = svc.AddWorklog(ctx, &u, key, task.WorklogInput{Duration: "1h"})
	require.NoError(t, err)
	require.Equal(t, "2026-10-10", v.WorkDate.Format(time.DateOnly))
	d := "2026-10-11"
	_, err = svc.AddWorklog(ctx, &u, key, task.WorklogInput{Duration: "1h", WorkDate: &d})
	require.ErrorIs(t, err, apierr.BadRequest(apierr.CodeValidation, ""), "в UTC 11 октября ещё не наступило")

	bot := audit.WithSource(audit.WithActor(ctx, u.ID), audit.SourceBot)
	tg, err := svc.AddWorklog(bot, &u, key, task.WorklogInput{Duration: "15m"})
	require.NoError(t, err)
	require.Equal(t, task.WorklogTelegram, tg.Source)

	_, err = svc.UpdateWorklog(ctx, &u, v.ID, task.WorklogPatch{Duration: task.Opt[string]{Set: true, Value: ptr("2h")}})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteWorklog(ctx, &u, v.ID))
}

func ptr[T any](v T) *T { return &v }
