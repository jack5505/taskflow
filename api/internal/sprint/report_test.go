package sprint_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/sprint"
)

// 14-дневный спринт: 13–26 октября 2026.
const dates14 = `"start_date":"2026-10-13","end_date":"2026-10-26"`

func (f *fixture) snapshots(t *testing.T, s string) map[string][3]int {
	t.Helper()
	var rows []struct {
		Date                                      time.Time
		OpenTasks, OpenEstimateMinutes, DoneTasks int
	}
	require.NoError(t, f.db.Raw(`SELECT date, open_tasks, open_estimate_minutes, done_tasks
		FROM sprint_daily_snapshots WHERE sprint_id = ?`, s).Scan(&rows).Error)
	m := map[string][3]int{}
	for _, r := range rows {
		m[r.Date.Format(time.DateOnly)] = [3]int{r.OpenTasks, r.OpenEstimateMinutes, r.DoneTasks}
	}
	return m
}

func (f *fixture) worklog(t *testing.T, key string, userID uuid.UUID, minutes int, date string) {
	t.Helper()
	require.NoError(t, f.db.Exec(`INSERT INTO worklogs (task_id, user_id, minutes, work_date, source)
		SELECT id, ?, ?, ?, 'manual' FROM tasks WHERE key = ?`, userID, minutes, date, key).Error)
}

// §3.8: снимок делается в 00:00 по поясу проекта, а не сервера (UTC); повторный проход воркера не
// дублирует снимок; дни до start_date не снимаются.
func TestDailySnapshotByProjectTimezone(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.db.Model(&project.Project{}).Where("id = ?", f.p.ID).Update("timezone", "Asia/Tashkent").Error) // UTC+5
	s := f.sprint(t, `{`+dates14+`}`)
	a := f.task(t, s, "60")
	f.task(t, s, "30")
	f.task(t, s, "")
	rec, _ := f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code)
	ctx := context.Background()

	// 13.10 18:30 UTC = 23:30 в Ташкенте: день 13-го ещё идёт, «вчера» (12-е) раньше старта.
	_, err := sprint.Snapshot(ctx, f.db, time.Date(2026, 10, 13, 18, 30, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Empty(t, f.snapshots(t, s))

	// 13.10 19:05 UTC: по серверу (UTC) ещё 13-е, в Ташкенте уже 14-е 00:05 — снимок за 13-е.
	for range 3 {
		_, err = sprint.Snapshot(ctx, f.db, time.Date(2026, 10, 13, 19, 5, 0, 0, time.UTC))
		require.NoError(t, err)
	}
	require.Equal(t, map[string][3]int{"2026-10-13": {3, 90, 0}}, f.snapshots(t, s), "один снимок за дату")

	f.setStatus(t, a, "done")
	_, err = sprint.Snapshot(ctx, f.db, time.Date(2026, 10, 14, 19, 1, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, [3]int{2, 30, 1}, f.snapshots(t, s)["2026-10-14"])

	// Запланированный и завершённый спринты не снимаются.
	planned := f.sprint(t, `{"name":"План",`+dates14+`}`)
	_, err = sprint.Snapshot(ctx, f.db, time.Date(2026, 10, 20, 19, 1, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Empty(t, f.snapshots(t, planned))
}

// §5.6: отчёт — объём на старте и при закрытии, добавленные и убранные, списано по исполнителям,
// берндаун на 14 точек из снимков; снимок закрытия — в транзакции закрытия по поясу проекта.
func TestSprintReport(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.db.Model(&project.Project{}).Where("id = ?", f.p.ID).Update("timezone", "Asia/Tashkent").Error)
	memberU, member := f.user(t, project.RoleMember)
	s := f.sprint(t, `{`+dates14+`}`)

	rec, b := f.do(t, "GET", "/api/v1/sprints/"+s+"/report", f.manager, "")
	require.Equal(t, http.StatusConflict, rec.Code, "у запланированного отчёта нет")
	require.Equal(t, "planned", reason(b))

	done := f.task(t, s, "60")
	open := f.task(t, s, "30")
	removed := f.task(t, s, "")
	rec, _ = f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code)
	added := f.task(t, s, "120") // создана сразу в активном — «добавлено после старта»
	rec, _ = f.do(t, "POST", "/api/v1/tasks/"+removed+"/move", f.manager, `{"sprint_id":null,"after_task_id":null}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	ctx := context.Background()
	_, err := sprint.Snapshot(ctx, f.db, time.Date(2026, 10, 13, 19, 30, 0, 0, time.UTC)) // конец 13-го
	require.NoError(t, err)
	f.setStatus(t, done, "done")
	_, err = sprint.Snapshot(ctx, f.db, time.Date(2026, 10, 14, 19, 30, 0, 0, time.UTC)) // конец 14-го
	require.NoError(t, err)

	f.worklog(t, done, f.managerU.ID, 90, "2026-10-13")
	f.worklog(t, open, memberU.ID, 30, "2026-10-14")
	f.worklog(t, open, memberU.ID, 15, "2026-10-16")       // день закрытия по Ташкенту — считается
	f.worklog(t, open, memberU.ID, 600, "2026-10-17")      // после закрытия, задача уже в бэклоге
	f.worklog(t, removed, f.managerU.ID, 45, "2026-10-14") // убрана после старта, но работа — в дни спринта
	f.worklog(t, open, memberU.ID, 100, "2026-10-12")      // до старта спринта — не считается

	rec, _ = f.do(t, "GET", "/api/v1/sprints/"+s+"/report", f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "не участник — 404")

	// Закрытие 15.10 20:00 UTC = 16.10 01:00 в Ташкенте: снимок закрытия — за 16-е.
	svc := sprint.NewService(f.db, project.NewService(f.db)).
		WithClock(func() time.Time { return time.Date(2026, 10, 15, 20, 0, 0, 0, time.UTC) })
	_, _, err = svc.Close(ctx, f.managerU, uuid.MustParse(s), nil)
	require.NoError(t, err)
	require.Equal(t, [3]int{2, 150, 1}, f.snapshots(t, s)["2026-10-16"], "до переноса: open и added незакрыты")

	rec, b = f.do(t, "GET", "/api/v1/sprints/"+s+"/report", member, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, map[string]any{"tasks": 3.0, "estimate_minutes": 90.0, "without_estimate": 1.0}, b["start"])
	require.Equal(t, map[string]any{"tasks": 3.0, "estimate_minutes": 210.0}, b["current"], "на момент закрытия: done + две перенесённые")

	addedList := b["added"].([]any)
	require.Len(t, addedList, 1)
	require.Equal(t, added, addedList[0].(map[string]any)["key"])
	removedList := b["removed"].([]any)
	require.Len(t, removedList, 1)
	require.Equal(t, removed, removedList[0].(map[string]any)["key"])
	require.Equal(t, f.managerU.ID.String(), removedList[0].(map[string]any)["by"])

	require.EqualValues(t, 180, b["spent_minutes"], "работа до старта и после закрытия не считается")
	spent := b["spent"].([]any)
	require.Len(t, spent, 2)
	require.Equal(t, f.managerU.ID.String(), spent[0].(map[string]any)["user_id"])
	require.EqualValues(t, 135, spent[0].(map[string]any)["minutes"])
	require.EqualValues(t, 2.25, spent[0].(map[string]any)["hours"])
	require.Equal(t, memberU.ID.String(), spent[1].(map[string]any)["user_id"])
	require.EqualValues(t, 45, spent[1].(map[string]any)["minutes"])

	bd := b["burndown"].([]any)
	require.Len(t, bd, 14, "14-дневный спринт — 14 точек")
	point := func(i int) map[string]any { return bd[i].(map[string]any) }
	require.Equal(t, "2026-10-13", point(0)["date"])
	require.Equal(t, "2026-10-26", point(13)["date"])
	require.EqualValues(t, 1.39, point(0)["ideal_hours"], "90 мин × 13/14")
	require.EqualValues(t, 0, point(13)["ideal_hours"])
	require.EqualValues(t, 3.5, point(0)["remaining_hours"], "60 + 30 + 120 минут, убранной нет")
	require.EqualValues(t, 2.5, point(1)["remaining_hours"])
	require.EqualValues(t, 1, point(1)["done_tasks"])
	require.EqualValues(t, 2.5, point(2)["remaining_hours"], "за 15-е воркер не успел — снимок сделан при закрытии")
	require.EqualValues(t, 2.5, point(3)["remaining_hours"], "снимок закрытия")
	require.Nil(t, point(4)["remaining_hours"])
}

// Ревью #109: закрытие сразу после полуночи по поясу проекта, раньше прохода воркера, — снимок
// за вчера делается в транзакции закрытия, точка последнего дня не пропадает.
func TestCloseJustAfterMidnightSnapshotsYesterday(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.db.Model(&project.Project{}).Where("id = ?", f.p.ID).Update("timezone", "Asia/Tashkent").Error)
	s := f.sprint(t, `{`+dates14+`}`)
	f.task(t, s, "60")
	rec, _ := f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code)
	svc := sprint.NewService(f.db, project.NewService(f.db)).
		WithClock(func() time.Time { return time.Date(2026, 10, 26, 19, 0, 20, 0, time.UTC) }) // 27.10 00:00:20 в Ташкенте
	_, _, err := svc.Close(context.Background(), f.managerU, uuid.MustParse(s), nil)
	require.NoError(t, err)
	require.Equal(t, map[string][3]int{"2026-10-26": {1, 60, 0}, "2026-10-27": {1, 60, 0}}, f.snapshots(t, s))
}

// Активный спринт: «сейчас» — задачи спринта в данный момент; без снимков точки пустые.
func TestSprintReportActive(t *testing.T) {
	f := newFixture(t)
	s := f.sprint(t, `{`+dates+`}`)
	f.task(t, s, "45")
	rec, _ := f.do(t, "POST", "/api/v1/sprints/"+s+"/start", f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code)
	f.task(t, s, "")
	rec, b := f.do(t, "GET", "/api/v1/sprints/"+s+"/report", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, map[string]any{"tasks": 2.0, "estimate_minutes": 45.0}, b["current"])
	require.Len(t, b["burndown"], 12, "13–24 октября")
	require.Empty(t, b["spent"])
	require.Empty(t, b["removed"])
	rec, _ = f.do(t, "GET", "/api/v1/sprints/not-a-uuid/report", f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}
