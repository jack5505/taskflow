package loadgen_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/loadgen"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

var today = time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)

func countIndexes(t *testing.T, q func(string, ...any) int64) int64 {
	t.Helper()
	return q(`SELECT count(*) FROM pg_indexes WHERE tablename IN ('tasks', 'worklogs')`)
}

// Малый объём тем же генератором: связи, суммы, спринты, роли и пароль — как обещает Params.
func TestGenerateSmall(t *testing.T) {
	ctx := context.Background()
	g, _ := testutil.TempDB(t)
	q := func(sql string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, g.Raw(sql, args...).Scan(&n).Error, sql)
		return n
	}
	indexesBefore := countIndexes(t, q)

	const password = "loadtest-password"
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)
	p := loadgen.Params{Users: 30, Projects: 4, Tasks: 1003, Worklogs: 5000, PasswordHash: hash, Today: today}
	var steps int
	require.NoError(t, loadgen.Generate(ctx, g, p, func(string, ...any) { steps++ }))
	require.Positive(t, steps)

	require.EqualValues(t, 30, q(`SELECT count(*) FROM users`))
	require.EqualValues(t, 1, q(`SELECT count(*) FROM users WHERE is_system_admin AND email = ?`, loadgen.Email(1)))
	require.EqualValues(t, 4, q(`SELECT count(*) FROM projects`))
	require.EqualValues(t, 1003, q(`SELECT count(*) FROM tasks`))
	require.EqualValues(t, 5000, q(`SELECT count(*) FROM worklogs`))
	require.Equal(t, indexesBefore, countIndexes(t, q), "индексы построены заново")

	// Записи — за 365 дней по Today; spent_minutes — сумма записей задачи.
	require.EqualValues(t, 0, q(`SELECT count(*) FROM worklogs WHERE work_date > ? OR work_date <= ?::date - 365`,
		today.Format(time.DateOnly), today.Format(time.DateOnly)))
	require.EqualValues(t, 0, q(`SELECT count(*) FROM tasks t WHERE spent_minutes <>
		(SELECT COALESCE(SUM(minutes), 0) FROM worklogs w WHERE w.task_id = t.id)`))
	// Время списывают и задачи получают только участники проекта задачи; за день у пользователя не
	// больше 24h (§6.3).
	require.EqualValues(t, 0, q(`SELECT count(*) FROM worklogs w JOIN tasks t ON t.id = w.task_id
		WHERE NOT EXISTS (SELECT 1 FROM project_members m WHERE m.project_id = t.project_id AND m.user_id = w.user_id)`))
	require.EqualValues(t, 0, q(`SELECT count(*) FROM tasks t WHERE assignee_id IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM project_members m WHERE m.project_id = t.project_id AND m.user_id = t.assignee_id)`))
	require.EqualValues(t, 803, q(`SELECT count(*) FROM tasks WHERE assignee_id IS NOT NULL`), "без исполнителя — каждая пятая")
	require.EqualValues(t, 0, q(`SELECT count(*) FROM (SELECT 1 FROM worklogs GROUP BY user_id, work_date HAVING sum(minutes) > 1440) d`))
	require.Greater(t, q(`SELECT count(DISTINCT user_id) FROM worklogs`), int64(20), "записи по многим пользователям")
	// Счётчик проекта — последний номер, ключи вида LAA-1; новая задача получит следующий номер.
	require.EqualValues(t, 0, q(`SELECT count(*) FROM projects p WHERE task_counter <>
		(SELECT max(number) FROM tasks t WHERE t.project_id = p.id)`))
	require.EqualValues(t, 251, q(`SELECT task_counter FROM projects WHERE key = ?`, loadgen.ProjectKey(0)))
	require.EqualValues(t, 250, q(`SELECT task_counter FROM projects WHERE key = ?`, loadgen.ProjectKey(3)))
	require.EqualValues(t, 1, q(`SELECT count(*) FROM tasks WHERE key = ?`, loadgen.ProjectKey(1)+"-1"))

	// Доска: в каждом проекте один активный спринт с последними SprintTasks задачами.
	require.EqualValues(t, 4, q(`SELECT count(*) FROM sprints WHERE state = 'active'`))
	require.EqualValues(t, 4*loadgen.SprintTasks, q(`SELECT count(*) FROM tasks WHERE sprint_id IS NOT NULL`))
	require.EqualValues(t, 0, q(`SELECT count(*) FROM tasks t JOIN sprints s ON s.id = t.sprint_id WHERE s.project_id <> t.project_id`))
	require.EqualValues(t, 4, q(`SELECT count(DISTINCT status) FROM tasks WHERE sprint_id IS NOT NULL`))
	require.EqualValues(t, 8, q(`SELECT count(*) FROM tasks WHERE deleted_at IS NOT NULL`), "каждая сотая по номеру")

	// Роли: 2 — менеджер везде, 3 — исполнитель везде, 4 — менеджер в половине; дальше по три проекта.
	role := func(n int, r string) int64 {
		return q(`SELECT count(*) FROM project_members m JOIN users u ON u.id = m.user_id WHERE u.email = ? AND m.role = ?`, loadgen.Email(n), r)
	}
	require.EqualValues(t, 4, role(2, "manager"))
	require.EqualValues(t, 4, role(3, "member"))
	require.EqualValues(t, 2, role(4, "manager"))
	require.EqualValues(t, 2, role(4, "member"))
	require.EqualValues(t, 3, role(5, "member"))
	require.EqualValues(t, 3, role(10, "manager"))
	require.EqualValues(t, 0, role(1, "member")+role(1, "manager"), "администратор видит всё без членства")

	// Пароль из Params подходит для входа.
	cfg := &config.Config{BaseURL: "https://tasks.example", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: time.Minute, JWTRefreshTTL: time.Hour}
	rdb := queue.NewRedis(testutil.SharedRedisAddr(t))
	t.Cleanup(func() { _ = rdb.Close() })
	s, err := auth.NewService(cfg, g, rdb).Login(ctx, loadgen.Email(7), password, "test")
	require.NoError(t, err)
	require.NotEmpty(t, s.AccessToken)

	// Повторный запуск не смешивает данные.
	require.ErrorIs(t, loadgen.Generate(ctx, g, p, func(string, ...any) {}), loadgen.ErrNotEmpty)
	require.EqualValues(t, 30, q(`SELECT count(*) FROM users`))
}

// Прерванная генерация (Ctrl+C) не оставляет на соединении пула режим replica и временные таблицы.
func TestGenerateCancelledLeavesConnectionClean(t *testing.T) {
	g, _ := testutil.TempDB(t)
	sqlDB, err := g.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // дальше — то же соединение, на котором шла генерация

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := loadgen.Params{Users: 10, Projects: 2, Tasks: 1000, Worklogs: 20_000_000, PasswordHash: "x", Today: today}
	err = loadgen.Generate(ctx, g, p, func(_ string, args ...any) {
		// Сразу после t_map: следующий шаг — запись миллионов строк, её и прерываем.
		if len(args) > 0 && strings.HasPrefix(fmt.Sprint(args[0]), "CREATE TEMP TABLE t_map") {
			go func() { time.Sleep(200 * time.Millisecond); cancel() }()
		}
	})
	require.ErrorIs(t, err, context.Canceled)

	var role string
	require.NoError(t, g.Raw(`SHOW session_replication_role`).Scan(&role).Error)
	require.Equal(t, "origin", role)
	var temps int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM pg_class WHERE relname IN ('u_map', 't_map', 'pm_map') AND relnamespace = pg_my_temp_schema()`).Scan(&temps).Error)
	require.Zero(t, temps)
}

func TestGenerateValidatesParams(t *testing.T) {
	ok := loadgen.Params{Users: 4, Projects: 1, Tasks: 1, PasswordHash: "x", Today: today}
	for name, mutate := range map[string]func(*loadgen.Params){
		"мало пользователей": func(p *loadgen.Params) { p.Users = 3 },
		"нет проектов":       func(p *loadgen.Params) { p.Projects = 0 },
		"много проектов":     func(p *loadgen.Params) { p.Projects = 26*26 + 1 },
		"задач меньше":       func(p *loadgen.Params) { p.Tasks = 0 },
		"записей меньше 0":   func(p *loadgen.Params) { p.Worklogs = -1 },
		"без пароля":         func(p *loadgen.Params) { p.PasswordHash = "" },
		"без даты":           func(p *loadgen.Params) { p.Today = time.Time{} },
	} {
		p := ok
		mutate(&p)
		// База не нужна: параметры проверяются до подключения.
		require.Error(t, loadgen.Generate(context.Background(), nil, p, nil), name)
	}
}

func TestProjectKeyAndEmail(t *testing.T) {
	require.Equal(t, "LAA", loadgen.ProjectKey(0))
	require.Equal(t, "LBX", loadgen.ProjectKey(49))
	require.Equal(t, "load42@example.com", loadgen.Email(42))
}
