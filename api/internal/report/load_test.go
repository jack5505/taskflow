package report_test

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/report"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
	"github.com/jack5505/taskflow/api/migrations"
)

// Объём §10.1: 200 пользователей, 50 проектов, 100 000 задач, 1 000 000 записей времени за год.
const (
	loadUsers    = 200
	loadProjects = 50
	loadTasks    = 100_000
	loadWorklogs = 1_000_000
)

// TestReportLoad — §10.1 и критерий issue #34: отчёт за месяц при 1 000 000 записей быстрее 3 с.
// Отдельная база в общем контейнере пакета (данные не мешают остальным тестам, удаляется в конце),
// строки генерирует сам PostgreSQL через generate_series. Время меряется по вызову сервиса: SQL,
// раскладка по дням, CSV.
func TestReportLoad(t *testing.T) {
	ctx := context.Background()
	shared := testutil.SharedDB(t)
	name := "report_load_" + strconv.FormatInt(testutil.Next(), 10)
	require.NoError(t, shared.Exec(`CREATE DATABASE `+name).Error)
	u, err := url.Parse(testutil.SharedDSN(t))
	require.NoError(t, err)
	u.Path = "/" + name
	dsn := u.String()
	mctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	require.NoError(t, db.Migrate(mctx, dsn, migrations.FS))
	g, err := db.Open(mctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close(g)
		_ = shared.Exec(`DROP DATABASE ` + name + ` WITH (FORCE)`).Error
	})
	sqlDB, err := g.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // временные таблицы u_map и t_map живут в одном соединении

	// Вторичные индексы worklogs из миграций снимаются на время загрузки и строятся заново по тем
	// же определениям — так в разы быстрее.
	var indexes []string
	require.NoError(t, g.Raw(`SELECT indexdef FROM pg_indexes WHERE tablename = 'worklogs'
		AND indexname <> 'worklogs_pkey' ORDER BY indexname`).Scan(&indexes).Error)
	require.Len(t, indexes, 2)
	started := time.Now()
	for _, q := range []string{
		`DROP INDEX worklogs_task_idx, worklogs_user_date_idx`,
		// Чистая сгенерированная база: внешние ключи при загрузке не проверяем (триггеры FK
		// удвоили бы время), связи и так верны по построению.
		`SET session_replication_role = replica`,
		`CREATE TEMP TABLE u_map AS SELECT n, gen_random_uuid() AS id FROM generate_series(1, ` + strconv.Itoa(loadUsers) + `) n`,
		`INSERT INTO users (id, email, password_hash, display_name, is_system_admin)
			SELECT id, 'load' || n || '@example.com', 'x', 'Пользователь ' || n, n = 1 FROM u_map`,
		`INSERT INTO projects (key, name) SELECT 'L' || chr(65 + n / 26) || chr(65 + n % 26), 'Проект ' || n
			FROM generate_series(0, ` + strconv.Itoa(loadProjects-1) + `) n`,
		// Пользователь 2 — менеджер всех проектов, 3 — исполнитель во всех, 4 — менеджер в половине
		// проектов и исполнитель в остальных.
		`INSERT INTO project_members (project_id, user_id, role)
			SELECT p.id, u.id, CASE WHEN u.n = 2 OR (u.n = 4 AND p.key < 'LB') THEN 'manager' ELSE 'member' END
			FROM projects p, u_map u WHERE u.n IN (2, 3, 4)`,
		`CREATE TEMP TABLE t_map AS SELECT n, gen_random_uuid() AS id FROM generate_series(0, ` + strconv.Itoa(loadTasks-1) + `) n`,
		// Сначала записи, потом задачи: spent_minutes (сумма записей) — сразу при вставке.
		`INSERT INTO worklogs (task_id, user_id, minutes, work_date, source)
			SELECT t.id, u.id, 1 + g % 120, DATE '2026-10-11' - (g % 365), 'manual'
			FROM generate_series(1, ` + strconv.Itoa(loadWorklogs) + `) g
			JOIN t_map t ON t.n = (g::bigint * 7919) % ` + strconv.Itoa(loadTasks) + `
			JOIN u_map u ON u.n = 1 + g % ` + strconv.Itoa(loadUsers),
		`INSERT INTO tasks (id, project_id, number, key, title, reporter_id, assignee_id, estimate_minutes, spent_minutes, position, deleted_at)
			SELECT m.id, p.id, m.n / ` + strconv.Itoa(loadProjects) + ` + 1, p.key || '-' || (m.n / ` + strconv.Itoa(loadProjects) + ` + 1),
				'Задача ' || m.n, u.id, u.id, 480, COALESCE(s.m, 0), m.n, CASE WHEN m.n % 100 = 0 THEN now() END
			FROM t_map m
			JOIN (SELECT id, key, row_number() OVER (ORDER BY key) - 1 AS i FROM projects) p ON p.i = m.n % ` + strconv.Itoa(loadProjects) + `
			JOIN u_map u ON u.n = 1 + m.n % ` + strconv.Itoa(loadUsers) + `
			LEFT JOIN (SELECT task_id, SUM(minutes) AS m FROM worklogs GROUP BY task_id) s ON s.task_id = m.id`,
		indexes[0], indexes[1],
		`SET session_replication_role = DEFAULT`,
		`VACUUM ANALYZE`,
	} {
		step := time.Now()
		require.NoError(t, g.Exec(q).Error, q)
		t.Logf("%s… %s", strings.Fields(q)[0], time.Since(step).Round(time.Millisecond))
	}
	var n int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM worklogs`).Scan(&n).Error)
	require.EqualValues(t, loadWorklogs, n)
	t.Logf("генерация %d записей: %s", n, time.Since(started).Round(time.Millisecond))

	actor := func(i int) *user.User {
		var u user.User
		require.NoError(t, g.Raw(`SELECT u.* FROM users u JOIN u_map m ON m.id = u.id WHERE m.n = ?`, i).Scan(&u).Error)
		return &u
	}
	admin, manager, member, mixed := actor(1), actor(2), actor(3), actor(4)
	var key string
	require.NoError(t, g.Raw(`SELECT min(key) FROM projects`).Scan(&key).Error)

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
