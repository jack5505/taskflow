package task_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// working — задача f.member в статусе in_progress.
func (f *fixture) working(t *testing.T) string {
	t.Helper()
	key := f.assigned(t)
	rec, _ := f.do(t, "POST", "/api/v1/tasks/"+key+"/transition", f.member, `{"to":"in_progress"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return key
}

func (f *fixture) startTimer(t *testing.T, key, token string, want int) map[string]any {
	t.Helper()
	rec, m := f.do(t, "POST", "/api/v1/tasks/"+key+"/timer/start", token, "")
	require.Equal(t, want, rec.Code, rec.Body.String())
	return m
}

// backdate сдвигает старт таймера пользователя f.member так, чтобы остановка сразу после
// дала ровно d с округлением вверх: на d − 30 секунд назад.
func (f *fixture) backdate(t *testing.T, d time.Duration) {
	t.Helper()
	require.NoError(t, f.db.Exec(`UPDATE active_timers SET started_at = started_at - ? * interval '1 second' WHERE user_id = ?`,
		int((d-30*time.Second)/time.Second), f.memberU.ID).Error)
}

func (f *fixture) timerLogs(t *testing.T, key string) []task.Worklog {
	t.Helper()
	var list []task.Worklog
	require.NoError(t, f.db.Raw(`SELECT w.* FROM worklogs w JOIN tasks t ON t.id = w.task_id
		WHERE t.key = ? AND w.source = 'timer' ORDER BY w.work_date`, key).Scan(&list).Error)
	return list
}

func (f *fixture) timerCount(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM active_timers WHERE user_id = ?`, f.memberU.ID).Scan(&n).Error)
	return n
}

func (f *fixture) events(t *testing.T, typ, key string) []map[string]any {
	t.Helper()
	var rows []string
	require.NoError(t, f.db.Raw(`SELECT payload::text FROM outbox_events WHERE type = ? AND payload->>'key' = ? ORDER BY created_at`, typ, key).
		Scan(&rows).Error)
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		require.NoError(t, json.Unmarshal([]byte(r), &out[i]))
	}
	return out
}

// deliver прогоняет события typ задачи key через обработчики уведомлений и возвращает число
// уведомлений notifType у f.member (§7.7).
func (f *fixture) deliver(t *testing.T, typ, key, notifType string) int64 {
	t.Helper()
	var rows []struct {
		ID      string
		Payload string
	}
	require.NoError(t, f.db.Raw(`SELECT id::text AS id, payload::text AS payload FROM outbox_events WHERE type = ? AND payload->>'key' = ?`,
		typ, key).Scan(&rows).Error)
	d := events.NewDispatcher(nil)
	notification.Register(d, f.db)
	for _, r := range rows {
		b, err := json.Marshal(events.Envelope{EventID: r.ID, Type: typ, Payload: []byte(r.Payload)})
		require.NoError(t, err)
		require.NoError(t, d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+typ, b)))
	}
	var n int64
	require.NoError(t, f.db.Model(&notification.Notification{}).Where("user_id = ? AND type = ?", f.memberU.ID, notifType).Count(&n).Error)
	return n
}

// Старт, таймер в GET /me, повторный старт, остановка с записью source = timer (округление вверх).
func TestTimerStartStop(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)

	m := f.startTimer(t, key, f.member, http.StatusOK)
	require.Equal(t, key, m["timer"].(map[string]any)["task_key"])
	require.Nil(t, m["stopped"])

	rec, me := f.do(t, "GET", "/api/v1/me", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, key, me["timer"].(map[string]any)["task_key"])
	_, me = f.do(t, "GET", "/api/v1/me", f.manager, "")
	require.Nil(t, me["timer"])

	m = f.startTimer(t, key, f.member, http.StatusConflict)
	require.Equal(t, apierr.CodeTimerAlreadyRunning, errCode(m))

	f.backdate(t, 47*time.Minute)
	rec, m = f.do(t, "POST", "/api/v1/me/timer/stop", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.EqualValues(t, 47, m["minutes"], "округление вверх до минуты")
	require.Equal(t, "47m", m["duration"])
	require.Equal(t, false, m["trimmed"])
	require.Len(t, m["worklogs"], 1)
	logs := f.timerLogs(t, key)
	require.Len(t, logs, 1)
	require.Equal(t, 47, logs[0].Minutes)
	require.Equal(t, today(), logs[0].WorkDate.Format(time.DateOnly))
	spent, _ := f.spent(t, key)
	require.Equal(t, 47, spent)
	require.Len(t, f.events(t, events.WorklogCreated, key), 1)

	rec, m = f.do(t, "POST", "/api/v1/me/timer/stop", f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, apierr.CodeNotFound, errCode(m))
	_, me = f.do(t, "GET", "/api/v1/me", f.member, "")
	require.Nil(t, me["timer"])
}

// §6.4: запускает только исполнитель задачи в работе; порядок 404 → 403 → 409.
func TestTimerStartRules(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	f.startTimer(t, key, f.outsider, http.StatusNotFound)
	m := f.startTimer(t, key, f.manager, http.StatusForbidden)
	require.Equal(t, apierr.CodeForbidden, errCode(m), "менеджер — не исполнитель")
	f.startTimer(t, "TM-0", f.member, http.StatusNotFound)

	todo := f.assigned(t)
	m = f.startTimer(t, todo, f.member, http.StatusConflict)
	require.Equal(t, apierr.CodeInvalidTransition, errCode(m))

	require.NoError(t, f.db.Model(&project.Project{}).Where("id = ?", f.p.ID).Update("is_archived", true).Error)
	m = f.startTimer(t, key, f.member, http.StatusConflict)
	require.Equal(t, apierr.CodeProjectArchived, errCode(m))
	require.Zero(t, f.timerCount(t))
}

// Сценарий приёмки: старт на одной задаче, старт на другой — первый остановлен, запись source = timer.
func TestTimerSwitchStopsPrevious(t *testing.T) {
	f := newFixture(t)
	a, b := f.working(t), f.working(t)
	f.startTimer(t, a, f.member, http.StatusOK)
	f.backdate(t, 10*time.Minute)
	m := f.startTimer(t, b, f.member, http.StatusOK)
	require.Equal(t, b, m["timer"].(map[string]any)["task_key"])
	stopped := m["stopped"].(map[string]any)
	require.Equal(t, a, stopped["task_key"])
	require.EqualValues(t, 10, stopped["minutes"])
	require.Len(t, f.timerLogs(t, a), 1)
	require.Equal(t, 1, f.timerCount(t))
}

// Меньше минуты — записи нет.
func TestTimerUnderMinuteWritesNothing(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	f.startTimer(t, key, f.member, http.StatusOK)
	f.backdate(t, 50*time.Second)
	rec, m := f.do(t, "POST", "/api/v1/me/timer/stop", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.EqualValues(t, 0, m["minutes"])
	require.Empty(t, m["worklogs"])
	require.Empty(t, f.timerLogs(t, key))
}

// clocked — сервис с подменёнными часами и таймер f.member на key, запущенный в started.
func (f *fixture) clocked(t *testing.T, key string, started time.Time, now *time.Time) *task.Service {
	t.Helper()
	require.NoError(t, f.db.Exec(`INSERT INTO active_timers (user_id, task_id, started_at)
		SELECT ?, id, ? FROM tasks WHERE key = ?`, f.memberU.ID, started, key).Error)
	return task.NewService(f.db, project.NewService(f.db)).WithClock(func() time.Time { return *now })
}

// Критерий #33: таймер 23:50–00:10 по поясу пользователя — две записи на разные даты.
func TestTimerSplitsAtMidnight(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	msk := time.FixedZone("MSK", 3*3600) // пояс пользователя по умолчанию — Europe/Moscow
	now := time.Date(2026, 10, 12, 0, 10, 0, 0, msk)
	svc := f.clocked(t, key, time.Date(2026, 10, 11, 23, 50, 0, 0, msk), &now)

	res, err := svc.StopTimer(context.Background(), f.memberU)
	require.NoError(t, err)
	require.Equal(t, 20, res.Minutes)
	logs := f.timerLogs(t, key)
	require.Len(t, logs, 2)
	require.Equal(t, "2026-10-11", logs[0].WorkDate.Format(time.DateOnly))
	require.Equal(t, 10, logs[0].Minutes)
	require.Equal(t, "2026-10-12", logs[1].WorkDate.Format(time.DateOnly))
	require.Equal(t, 10, logs[1].Minutes)

	// Тот же интервал для пользователя в UTC (20:50–21:10) — одна запись.
	require.NoError(t, f.db.Exec(`UPDATE users SET timezone = 'UTC' WHERE id = ?`, f.memberU.ID).Error)
	key2 := f.working(t)
	svc = f.clocked(t, key2, time.Date(2026, 10, 11, 23, 50, 0, 0, msk), &now)
	_, err = svc.StopTimer(context.Background(), f.memberU)
	require.NoError(t, err)
	require.Len(t, f.timerLogs(t, key2), 1)
}

// §6.4: запись сверх дневного лимита обрезается до остатка (timer.trimmed); остатка нет — записи нет.
func TestTimerTrimmedByDailyLimit(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	msk := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 10, 11, 15, 0, 0, 0, msk)
	require.NoError(t, f.db.Exec(`INSERT INTO worklogs (task_id, user_id, minutes, work_date, source)
		SELECT id, ?, 1430, '2026-10-11', 'manual' FROM tasks WHERE key = ?`, f.memberU.ID, key).Error)

	svc := f.clocked(t, key, now.Add(-30*time.Minute), &now)
	res, err := svc.StopTimer(context.Background(), f.memberU)
	require.NoError(t, err)
	require.True(t, res.Trimmed)
	require.Equal(t, 10, res.Minutes)
	trimmed := f.events(t, events.TimerTrimmed, key)
	require.Len(t, trimmed, 1)
	require.EqualValues(t, 30, trimmed[0]["requested_minutes"])
	require.EqualValues(t, 10, trimmed[0]["written_minutes"])

	svc = f.clocked(t, key, now.Add(-5*time.Minute), &now)
	res, err = svc.StopTimer(context.Background(), f.memberU)
	require.NoError(t, err)
	require.True(t, res.Trimmed)
	require.Zero(t, res.Minutes)
	require.Empty(t, res.Worklogs)
	require.Len(t, f.timerLogs(t, key), 1, "остатка нет — запись не создаётся")
	require.Len(t, f.events(t, events.TimerTrimmed, key), 2)
	require.EqualValues(t, 2, f.deliver(t, events.TimerTrimmed, key, notification.TypeTimerTrimmed), "§7.7: колокольчик")
}

// Критерий #33: автостоп через 12 часов со сдвигом времени — запись ровно 12h, timer.auto_stopped.
func TestTimerAutoStopAfter12Hours(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	msk := time.FixedZone("MSK", 3*3600)
	started := time.Date(2026, 10, 11, 8, 0, 0, 0, msk)
	now := started.Add(11*time.Hour + 59*time.Minute)
	svc := f.clocked(t, key, started, &now)

	n, err := svc.StopStaleTimers(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "меньше 12 часов — таймер идёт")
	require.Equal(t, 1, f.timerCount(t))

	now = started.Add(13 * time.Hour)
	n, err = svc.StopStaleTimers(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.Zero(t, f.timerCount(t))
	logs := f.timerLogs(t, key)
	require.Len(t, logs, 1)
	require.Equal(t, 720, logs[0].Minutes, "остановлен на отметке 12 часов")
	stopped := f.events(t, events.TimerAutoStopped, key)
	require.Len(t, stopped, 1)
	require.Equal(t, f.memberU.ID.String(), stopped[0]["user_id"])
	require.EqualValues(t, 720, stopped[0]["minutes"])
	require.EqualValues(t, 1, f.deliver(t, events.TimerAutoStopped, key, notification.TypeTimerStopped), "§7.7: колокольчик")
}

// Обход воркера останавливает таймеры архивного проекта — сразу, без ожидания 12 часов.
func TestTimerStoppedInArchivedProject(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	f.startTimer(t, key, f.member, http.StatusOK)
	f.backdate(t, 5*time.Minute)
	svc := task.NewService(f.db, project.NewService(f.db))
	n, err := svc.StopStaleTimers(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)

	rec, _ := f.do(t, "PATCH", "/api/v1/projects/"+f.p.Key, f.admin, `{"is_archived":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var at time.Time
	require.NoError(t, f.db.Raw(`SELECT (payload->>'at')::timestamptz FROM outbox_events WHERE type = ? AND payload->>'project_id' = ?`,
		events.ProjectArchived, f.p.ID.String()).Scan(&at).Error)
	require.False(t, at.IsZero(), "момент архивации — в событии")

	// Воркер обработал событие позже — время пишется до момента архивации.
	n, err = svc.StopProjectTimers(context.Background(), f.p.ID, at)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	logs := f.timerLogs(t, key)
	require.Len(t, logs, 1)
	require.Equal(t, 5, logs[0].Minutes)
	require.Empty(t, f.events(t, events.TimerAutoStopped, key), "не автостоп по 12 часам")
	n, err = svc.StopStaleTimers(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
}

// Старт на той же задаче, когда таймер уже старше 12 часов, а воркер его ещё не погасил:
// старый останавливается на отметке 12 часов, запускается новый — не 409.
func TestTimerRestartAfterLimit(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	msk := time.FixedZone("MSK", 3*3600)
	started := time.Date(2026, 10, 11, 6, 0, 0, 0, msk)
	now := started.Add(12*time.Hour + 30*time.Minute)
	svc := f.clocked(t, key, started, &now)

	res, err := svc.StartTimer(context.Background(), f.memberU, key)
	require.NoError(t, err)
	require.NotNil(t, res.Stopped)
	require.True(t, res.Stopped.AutoStopped)
	require.Equal(t, 720, res.Stopped.Minutes)
	require.Equal(t, now, res.Timer.StartedAt.In(msk))
	require.Equal(t, 1, f.timerCount(t))
}

// §6.4, §4: уход из in_progress, смена исполнителя и удаление задачи останавливают таймер с записью.
func TestTimerStoppedByTaskChanges(t *testing.T) {
	f := newFixture(t)
	for name, act := range map[string]func(key string) (int, string){
		"review": func(key string) (int, string) {
			rec, _ := f.do(t, "POST", "/api/v1/tasks/"+key+"/transition", f.member, `{"to":"review"}`)
			return rec.Code, rec.Body.String()
		},
		"reassign": func(key string) (int, string) {
			rec, _ := f.patch(t, f.manager, key, `"`+strconv.Itoa(f.get(t, key).Version)+`"`, `{"assignee_id":"`+f.managerU.ID.String()+`"}`)
			return rec.Code, rec.Body.String()
		},
		"delete": func(key string) (int, string) {
			rec, _ := f.do(t, "DELETE", "/api/v1/tasks/"+key, f.manager, "")
			return rec.Code, rec.Body.String()
		},
	} {
		t.Run(name, func(t *testing.T) {
			key := f.working(t)
			f.startTimer(t, key, f.member, http.StatusOK)
			f.backdate(t, 3*time.Minute)
			code, body := act(key)
			require.Less(t, code, 300, body)
			require.Zero(t, f.timerCount(t))
			logs := f.timerLogs(t, key)
			require.Len(t, logs, 1)
			require.Equal(t, 3, logs[0].Minutes)
		})
	}

	// Правка, не задевающая статус и исполнителя, таймер не трогает.
	key := f.working(t)
	f.startTimer(t, key, f.member, http.StatusOK)
	rec, _ := f.patch(t, f.member, key, `"`+strconv.Itoa(f.get(t, key).Version)+`"`, `{"title":"Новое название"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, f.timerCount(t))
}

// §3.1: деактивация останавливает таймер с записью времени.
func TestTimerStoppedOnDeactivation(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	f.startTimer(t, key, f.member, http.StatusOK)
	f.backdate(t, 7*time.Minute)
	rec, _ := f.do(t, "PATCH", "/api/v1/users/"+f.memberU.ID.String(), f.admin, `{"is_active":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Zero(t, f.timerCount(t))
	logs := f.timerLogs(t, key)
	require.Len(t, logs, 1)
	require.Equal(t, 7, logs[0].Minutes)
}

// Одновременные старты одного пользователя на разных задачах не дают второго таймера и 500.
func TestTimerConcurrentStarts(t *testing.T) {
	f := newFixture(t)
	keys := []string{f.working(t), f.working(t), f.working(t), f.working(t)}
	var wg sync.WaitGroup
	codes := make([]int, len(keys))
	for i, key := range keys {
		wg.Go(func() {
			rec, _ := f.do(t, "POST", "/api/v1/tasks/"+key+"/timer/start", f.member, "")
			codes[i] = rec.Code
		})
	}
	wg.Wait()
	for _, c := range codes {
		require.Equal(t, http.StatusOK, c)
	}
	require.Equal(t, 1, f.timerCount(t))
}
