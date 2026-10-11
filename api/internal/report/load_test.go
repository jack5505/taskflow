package report_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/loadgen"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/report"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// TestReportLoad — §10.3 и критерий issue #34: отчёт за месяц при 1 000 000 записей быстрее 3 с.
// Данные — генератор нагрузочного теста (loadgen) в отдельной базе общего контейнера пакета (данные
// не мешают остальным тестам, удаляется в конце). Время меряется по вызову сервиса: SQL, раскладка
// по дням, CSV.
func TestReportLoad(t *testing.T) {
	ctx := context.Background()
	g, _ := testutil.TempDB(t)
	started := time.Now()
	require.NoError(t, loadgen.Generate(ctx, g, loadgen.Params{
		Users: loadgen.DefaultUsers, Projects: loadgen.DefaultProjects,
		Tasks: loadgen.DefaultTasks, Worklogs: loadgen.DefaultWorklogs,
		PasswordHash: "x", Today: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
	}, t.Logf))
	var n int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM worklogs`).Scan(&n).Error)
	require.EqualValues(t, loadgen.DefaultWorklogs, n)
	t.Logf("генерация %d записей: %s", n, time.Since(started).Round(time.Millisecond))

	actor := func(i int) *user.User {
		var u user.User
		require.NoError(t, g.Raw(`SELECT * FROM users WHERE email = ?`, loadgen.Email(i)).Scan(&u).Error)
		return &u
	}
	admin, manager, member, mixed := actor(1), actor(2), actor(3), actor(4)
	key := loadgen.ProjectKey(0)

	svc := report.NewService(g, project.NewService(g))
	month := report.Query{From: "2026-09-01", To: "2026-09-30"}
	for _, c := range []struct {
		name string
		u    *user.User
		q    func(report.Query) report.Query
	}{
		{"администратор, по пользователям", admin, func(q report.Query) report.Query { return q }},
		{"администратор, по задачам", admin, func(q report.Query) report.Query { q.GroupBy = report.GroupByTask; return q }},
		{"менеджер, проект, по задачам", manager, func(q report.Query) report.Query {
			q.GroupBy, q.Project = report.GroupByTask, key
			return q
		}},
		{"менеджер, все проекты, по пользователям", manager, func(q report.Query) report.Query { return q }},
		{"менеджер и исполнитель, по задачам", mixed, func(q report.Query) report.Query { q.GroupBy = report.GroupByTask; return q }},
		{"исполнитель, по задачам", member, func(q report.Query) report.Query { q.GroupBy = report.GroupByTask; return q }},
	} {
		start := time.Now()
		r, err := svc.Time(ctx, c.u, c.q(month))
		require.NoError(t, err, c.name)
		require.NoError(t, report.WriteCSV(discard{}, r))
		spent := time.Since(start)
		t.Logf("%s: %s, записей на %d мин", c.name, spent.Round(time.Millisecond), r.TotalMinutes)
		require.Less(t, spent, 3*time.Second, c.name)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
