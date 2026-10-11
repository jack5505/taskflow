// Package loadgen — генератор тестовых данных для проверки нагрузки (ТЗ §10.3, §10.4): пользователи,
// проекты с участниками, активный спринт в каждом проекте, задачи и записи времени.
//
// Строки генерирует сам PostgreSQL через generate_series: 100 000 задач и 1 000 000 записей
// получаются примерно за минуту. Генератор пишет только в пустую базу — на рабочем стенде с
// настоящими проектами он откажется работать.
package loadgen

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Объём §10.3: 200 пользователей, 50 проектов, 100 000 задач, 1 000 000 записей времени за год.
const (
	DefaultUsers    = 200
	DefaultProjects = 50
	DefaultTasks    = 100_000
	DefaultWorklogs = 1_000_000

	// SprintTasks — задач каждого проекта в активном спринте (на доске): последние по номеру.
	SprintTasks = 100
)

// ErrNotEmpty — в базе уже есть пользователи или проекты: генератор не смешивает данные с настоящими.
var ErrNotEmpty = errors.New("база не пустая: генератор пишет только в пустую базу после миграций")

// Params — объём данных и пароль пользователей.
//
// Пользователи: load1@example.com — администратор системы; load2 — менеджер всех проектов;
// load3 — исполнитель во всех проектах; load4 — менеджер в первой половине проектов и исполнитель
// в остальных; load5… — участники трёх проектов (каждый десятый — менеджер в них). У всех один
// пароль, его bcrypt-хеш — PasswordHash.
type Params struct {
	Users, Projects, Tasks, Worklogs int
	PasswordHash                     string
	Today                            time.Time // записи времени — за 365 дней по эту дату включительно
}

// Email — логин сгенерированного пользователя с номером n (с 1).
func Email(n int) string { return fmt.Sprintf("load%d@example.com", n) }

// ProjectKey — ключ проекта с номером i (с 0): LAA, LAB, …
func ProjectKey(i int) string { return fmt.Sprintf("L%c%c", 'A'+i/26, 'A'+i%26) }

func (p Params) validate() error {
	switch {
	case p.Users < 4:
		return errors.New("пользователей не меньше 4: администратор, менеджер, исполнитель, смешанная роль")
	case p.Projects < 1 || p.Projects > 26*26:
		return fmt.Errorf("проектов от 1 до %d", 26*26)
	case p.Tasks < p.Projects:
		return errors.New("задач не меньше, чем проектов")
	case p.Worklogs < 0:
		return errors.New("записей времени не меньше 0")
	case p.PasswordHash == "":
		return errors.New("нужен хеш пароля")
	case p.Today.IsZero():
		return errors.New("нужна дата Today")
	}
	return nil
}

// Generate заполняет пустую базу с применёнными миграциями. Все шаги идут в одном соединении
// (временные таблицы u_map, pm_map и t_map), logf получает время каждого шага.
func Generate(ctx context.Context, g *gorm.DB, p Params, logf func(format string, args ...any)) error {
	if err := p.validate(); err != nil {
		return err
	}
	return g.WithContext(ctx).Connection(func(c *gorm.DB) error {
		var n int64
		if err := c.Raw(`SELECT (SELECT count(*) FROM users) + (SELECT count(*) FROM projects)`).Scan(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrNotEmpty
		}
		// Вторичные индексы worklogs и tasks из миграций снимаются на время загрузки и строятся
		// заново по тем же определениям — так в разы быстрее.
		var indexes []struct{ Name, Def string }
		// Индексы ограничений (первичный ключ, UNIQUE) остаются: их не снять через DROP INDEX.
		if err := c.Raw(`SELECT i.relname AS name, pg_get_indexdef(i.oid) AS def
			FROM pg_index x JOIN pg_class i ON i.oid = x.indexrelid JOIN pg_class t ON t.oid = x.indrelid
			WHERE t.relnamespace = current_schema()::regnamespace AND t.relname IN ('worklogs', 'tasks')
				AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.oid)
			ORDER BY i.relname`).Scan(&indexes).Error; err != nil {
			return err
		}
		steps := make([]string, 0, 16+2*len(indexes))
		for _, ix := range indexes {
			steps = append(steps, `DROP INDEX `+ix.Name)
		}
		steps = append(steps, p.insertSteps()...)
		for _, ix := range indexes {
			steps = append(steps, ix.Def)
		}
		steps = append(steps, `SET session_replication_role = DEFAULT`, `DROP TABLE u_map, t_map, pm_map`, `VACUUM ANALYZE`)

		started := time.Now()
		for _, q := range steps {
			step := time.Now()
			if err := c.Exec(q).Error; err != nil {
				// Соединение вернётся в пул: роль репликации и временные таблицы не должны остаться на
				// нём. Контекст мог быть отменён (Ctrl+C) — сброс идёт без отмены.
				_ = c.WithContext(context.WithoutCancel(ctx)).Exec(`DISCARD ALL`).Error
				return fmt.Errorf("%s: %w (база осталась наполовину заполненной — создайте её заново)", firstLine(q), err)
			}
			logf("%s… %s", firstLine(q), time.Since(step).Round(time.Millisecond))
		}
		logf("готово за %s", time.Since(started).Round(time.Second))
		return nil
	})
}

// insertSteps — вставки по порядку зависимостей. Числа и хеш пароля подставляются в текст: это
// параметры генератора, а не ввод пользователя, и так PostgreSQL видит числа при планировании.
func (p Params) insertSteps() []string {
	r := strings.NewReplacer(
		"{users}", fmt.Sprint(p.Users),
		"{projects}", fmt.Sprint(p.Projects),
		"{half}", fmt.Sprint((p.Projects+1)/2),
		"{tasks}", fmt.Sprint(p.Tasks),
		"{worklogs}", fmt.Sprint(p.Worklogs),
		"{sprint}", fmt.Sprint(SprintTasks),
		"{today}", p.Today.Format(time.DateOnly),
		"{password_hash}", quote(p.PasswordHash),
	)
	steps := []string{
		// Чистая сгенерированная база: внешние ключи при загрузке не проверяем (триггеры FK
		// удвоили бы время), связи и так верны по построению.
		`SET session_replication_role = replica`,
		`CREATE TEMP TABLE u_map AS SELECT n, gen_random_uuid() AS id FROM generate_series(1, {users}) n`,
		`INSERT INTO users (id, email, password_hash, display_name, is_system_admin)
			SELECT id, 'load' || n || '@example.com', {password_hash}, 'Пользователь ' || n, n = 1 FROM u_map`,
		// p — номер проекта с 0; task_counter — сколько задач у проекта получится ниже.
		`INSERT INTO projects (key, name, task_counter)
			SELECT 'L' || chr(65 + p / 26) || chr(65 + p % 26), 'Проект ' || (p + 1),
				{tasks} / {projects} + CASE WHEN p < {tasks} % {projects} THEN 1 ELSE 0 END
			FROM generate_series(0, {projects} - 1) p`,
		// Пользователь 2 — менеджер всех проектов, 3 — исполнитель во всех, 4 — менеджер в первой
		// половине проектов и исполнитель в остальных; 5 и дальше — в трёх проектах подряд по кругу.
		`INSERT INTO project_members (project_id, user_id, role)
			SELECT p.id, u.id, CASE WHEN u.n = 2 OR (u.n = 4 AND p.i < {half}) OR (u.n >= 5 AND u.n % 10 = 0)
				THEN 'manager' ELSE 'member' END
			FROM (SELECT id, row_number() OVER (ORDER BY key) - 1 AS i FROM projects) p
			JOIN u_map u ON u.n IN (2, 3, 4)
				OR (u.n >= 5 AND (p.i - u.n % {projects} + {projects}) % {projects} < LEAST(3, {projects}))`,
		// Участники проекта i по порядку (j с 0) — из них выбираются исполнители и авторы записей времени.
		`CREATE TEMP TABLE pm_map AS
			SELECT p.i, pm.user_id AS id, row_number() OVER (PARTITION BY p.i ORDER BY pm.user_id) - 1 AS j,
				count(*) OVER (PARTITION BY p.i) AS cnt
			FROM project_members pm JOIN (SELECT id, row_number() OVER (ORDER BY key) - 1 AS i FROM projects) p ON p.id = pm.project_id`,
		// Активный спринт каждого проекта: вторая неделя из двух.
		`INSERT INTO sprints (project_id, name, start_date, end_date, state)
			SELECT id, 'Спринт 1', DATE '{today}' - 7, DATE '{today}' + 7, 'active' FROM projects`,
		// Задача n: проект pi = n % projects и число его участников cnt — для равенств в соединениях ниже.
		`CREATE TEMP TABLE t_map AS SELECT n, gen_random_uuid() AS id, c.i AS pi, c.cnt
			FROM generate_series(0, {tasks} - 1) n JOIN (SELECT DISTINCT i, cnt FROM pm_map) c ON c.i = n % {projects}`,
		// Сначала записи, потом задачи: spent_minutes (сумма записей) — сразу при вставке. Время списывает
		// участник проекта задачи. k-я запись пользователя — на k-й день назад по кругу, так что в день у
		// него не больше ceil(cnt/365) записей, а длительность ограничена, чтобы сумма за день не
		// превысила 24h (§6.3); в среднем выходит около рабочего дня.
		`INSERT INTO worklogs (task_id, user_id, minutes, work_date, source)
			SELECT task_id, user_id, 1 + g % LEAST(120, 1440 / ((cnt + 364) / 365)), DATE '{today}' - ((k - 1) % 365)::int, 'manual'
			FROM (
				SELECT g, t.id AS task_id, m.id AS user_id,
					row_number() OVER (PARTITION BY m.id ORDER BY g) AS k, count(*) OVER (PARTITION BY m.id) AS cnt
				FROM generate_series(1, {worklogs}) g
				JOIN t_map t ON t.n = (g::bigint * 7919) % {tasks}
				JOIN pm_map m ON m.i = t.pi AND m.j = (g / 7) % t.cnt
			) x`,
		// Задача n — в проекте n % projects с номером n / projects + 1, исполнитель — участник проекта. Последние SprintTasks номеров
		// проекта — в активном спринте, остальные в бэклоге. Статус, тип и приоритет — по номеру,
		// каждая сотая задача удалена (корзина, отчёт по удалённым).
		`INSERT INTO tasks (id, project_id, number, key, title, type, status, priority, reporter_id, assignee_id,
				sprint_id, estimate_minutes, spent_minutes, position, started_at, completed_at, deleted_at)
			SELECT m.id, p.id, x.num, p.key || '-' || x.num, 'Задача ' || x.num || ' проекта ' || p.key,
				(ARRAY['task', 'bug', 'feature'])[1 + x.num % 3],
				(ARRAY['todo', 'in_progress', 'review', 'done', 'todo'])[1 + x.num % 5],
				(ARRAY['low', 'medium', 'high', 'critical'])[1 + x.num % 4],
				r.id, CASE WHEN x.num % 5 = 0 THEN NULL ELSE a.id END,
				CASE WHEN x.num > p.task_counter - {sprint} THEN s.id END,
				60 * (1 + x.num % 16), COALESCE(w.m, 0), x.num,
				CASE WHEN x.num % 5 IN (1, 2, 3) THEN now() END,
				CASE WHEN x.num % 5 = 3 THEN now() END,
				CASE WHEN x.num % 100 = 0 THEN now() END
			FROM t_map m
			CROSS JOIN LATERAL (SELECT (m.n / {projects} + 1)::int AS num) x
			JOIN (SELECT id, key, task_counter, row_number() OVER (ORDER BY key) - 1 AS i FROM projects) p ON p.i = m.n % {projects}
			JOIN sprints s ON s.project_id = p.id
			JOIN pm_map a ON a.i = m.pi AND a.j = x.num % m.cnt
			JOIN u_map r ON r.n = 2
			LEFT JOIN (SELECT task_id, SUM(minutes) AS m FROM worklogs GROUP BY task_id) w ON w.task_id = m.id`,
	}
	for i, s := range steps {
		steps[i] = r.Replace(s)
	}
	return steps
}

// quote — строковый литерал SQL. Хеш bcrypt состоит из [./$0-9A-Za-z], кавычки удваиваются на всякий случай.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func firstLine(q string) string {
	q, _, _ = strings.Cut(strings.TrimSpace(q), "\n")
	if len(q) > 80 {
		q = q[:80]
	}
	return q
}
