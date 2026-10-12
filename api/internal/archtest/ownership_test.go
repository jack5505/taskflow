package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// owners — карта владения таблицами (эпик #140, api/CLAUDE.md). Только модуль-владелец
// пишет SQL к своей таблице; остальные вызывают его экспортированные функции.
var owners = map[string]string{
	"users":                  "user",
	"refresh_tokens":         "auth",
	"password_tokens":        "auth",
	"projects":               "project",
	"project_members":        "project",
	"tasks":                  "task",
	"task_history":           "task",
	"comments":               "task",
	"attachments":            "task",
	"worklogs":               "task",
	"active_timers":          "task",
	"sprints":                "sprint",
	"sprint_log":             "sprint",
	"sprint_start_scope":     "sprint",
	"sprint_daily_snapshots": "sprint",
	"notifications":          "notification",
	"due_reminders":          "notification",
	"one_time_codes":         "telegram",
	"daily_summaries":        "telegram",
	"audit_logs":             "audit",
	"outbox_events":          "outbox",
	"github_deliveries":      "github",
}

// readAnyTable — постоянные исключения: модули, которым разрешено читать и писать любые
// таблицы. report — агрегирующие отчёты одним запросом ради производительности (ТЗ §10.3),
// loadgen — генератор тестовых данных. Новое исключение — только правкой этого списка
// и api/CLAUDE.md.
var readAnyTable = map[string]bool{
	"report":  true,
	"loadgen": true,
}

// notModules — пакеты, которые не модули предметной области и не проверяются: docs —
// сгенерированный Swagger (тексты описаний), testutil — фикстуры тестов.
var notModules = map[string]bool{
	"docs":     true,
	"testutil": true,
}

// moduleOf — пакеты, входящие в чужой модуль: events — запись событий в outbox_events
// (events.Add в транзакции вызывающего) — часть модуля outbox.
var moduleOf = map[string]string{
	"events": "outbox",
}

var (
	// sqlKeyword — место, после которого в SQL идёт имя таблицы.
	sqlKeyword = regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?|lock(?:\s+table)?)\b`)
	// tableRef — имя таблицы, возможно в кавычках и со схемой public, у начала строки.
	// Хвост «.» — это колонка (EXTRACT(EPOCH FROM tasks.created_at)), не таблица.
	tableRef = regexp.MustCompile(`(?i)^\s+(?:only\s+)?(?:"?public"?\.)?"?([a-z_][a-z0-9_]*)"?(\.)?`)
	// nextInList — продолжение списка FROM a x, b y: необязательный алиас и запятая.
	nextInList = regexp.MustCompile(`(?i)^(?:\s+(?:as\s+)?[a-z_][a-z0-9_]*)?\s*,`)
	// sqlLike — строка похожа на SQL или его фрагмент; тексты логов и ошибок («bot removed from
	// group, projects unbound») не проверяются.
	sqlLike = regexp.MustCompile(`(?i)\b(?:select|insert|update|delete|truncate|lock|join|where|returning)\b`)
)

// tablesIn — таблицы из owners, к которым обращается SQL s, по одной на каждое обращение.
// Имена, которых нет в owners (CTE, функции, алиасы), игнорируются.
func tablesIn(s string) []string {
	if !sqlLike.MatchString(s) {
		return nil
	}
	var out []string
	for _, loc := range sqlKeyword.FindAllStringIndex(s, -1) {
		rest := s[loc[1]:]
		for {
			m := tableRef.FindStringSubmatchIndex(rest)
			if m == nil {
				break
			}
			if m[4] < 0 { // не колонка и не функция
				if table := strings.ToLower(rest[m[2]:m[3]]); owners[table] != "" {
					out = append(out, table)
				}
			}
			rest = rest[m[1]:]
			n := nextInList.FindStringIndex(rest)
			if m[4] >= 0 || n == nil {
				break
			}
			rest = " " + rest[n[1]:]
		}
	}
	return out
}

// violation — обращение файла (путь от internal/) к чужой таблице.
type violation struct {
	file, table string
}

func (v violation) String() string { return v.file + " " + v.table }

const violationsFile = "temporary_violations.txt"

func TestTableOwnership(t *testing.T) {
	found := scan(t, "..")
	allowed := readViolations(t, violationsFile)

	var msgs []string
	for _, v := range sortedKeys(found) {
		owner := owners[v.table]
		switch n, ok := allowed[v]; {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("%s обращается к %s (владелец %s) — вызовите функцию модуля %s", v.file, v.table, owner, owner))
		case found[v] > n:
			msgs = append(msgs, fmt.Sprintf("%s: новое обращение к %s (владелец %s, было %d, стало %d) — вызовите функцию модуля %s", v.file, v.table, owner, n, found[v], owner))
		case found[v] < n:
			msgs = append(msgs, fmt.Sprintf("%s: обращений к %s стало меньше (%d вместо %d) — исправьте число в %s", v.file, v.table, found[v], n, violationsFile))
		}
	}
	for _, v := range sortedKeys(allowed) {
		if found[v] == 0 {
			msgs = append(msgs, fmt.Sprintf("нарушение «%s» исправлено — удалите строку из %s", v, violationsFile))
		}
	}
	if len(msgs) > 0 {
		t.Fatalf("владение таблицами (api/CLAUDE.md):\n%s", strings.Join(msgs, "\n"))
	}
}

// TestOwnersCoverMigrations — у каждой таблицы из миграций есть владелец: новая таблица
// без записи в owners не проверялась бы вовсе.
func TestOwnersCoverMigrations(t *testing.T) {
	files, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("миграции не найдены: %v", err)
	}
	sort.Strings(files) // имена начинаются с времени — порядок применения
	var sqls []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sqls = append(sqls, string(b))
	}
	tables := schemaTables(sqls...)
	for _, table := range sortedStrings(tables) {
		if owners[table] == "" {
			t.Errorf("таблица %s без владельца — добавьте её в owners и в api/CLAUDE.md", table)
		}
	}
	for _, table := range sortedStrings(owners) {
		if !tables[table] {
			t.Errorf("таблицы %s нет в миграциях — удалите её из owners", table)
		}
	}
}

var (
	sqlComment = regexp.MustCompile(`(?s)--[^\n]*|/\*.*?\*/`)
	ddl        = regexp.MustCompile(`(?i)\b(?:create\s+(?:unlogged\s+)?table\s+(?:if\s+not\s+exists\s+)?(?:public\.)?([a-z_][a-z0-9_]*)` +
		`|drop\s+table\s+(?:if\s+exists\s+)?([a-z0-9_.,\s]+?)\s*(?:cascade|restrict)?\s*;` +
		`|alter\s+table\s+(?:if\s+exists\s+)?(?:only\s+)?(?:public\.)?([a-z_][a-z0-9_]*)\s+rename\s+to\s+([a-z_][a-z0-9_]*))`)
)

// schemaTables — таблицы схемы после применения миграций по порядку: CREATE TABLE,
// DROP TABLE (в том числе списком), ALTER TABLE … RENAME TO; комментарии SQL не считаются.
func schemaTables(migrations ...string) map[string]bool {
	tables := map[string]bool{}
	for _, sql := range migrations {
		sql = sqlComment.ReplaceAllString(sql, " ")
		for _, m := range ddl.FindAllStringSubmatch(sql, -1) {
			switch {
			case m[1] != "":
				tables[strings.ToLower(m[1])] = true
			case m[2] != "":
				for _, name := range strings.Split(m[2], ",") {
					name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "public.")
					delete(tables, name)
				}
			case m[3] != "":
				delete(tables, strings.ToLower(m[3]))
				tables[strings.ToLower(m[4])] = true
			}
		}
	}
	return tables
}

func TestSchemaTables(t *testing.T) {
	got := schemaTables(
		"CREATE TABLE users (id uuid); -- CREATE TABLE ghost (id int)\nCREATE UNLOGGED TABLE IF NOT EXISTS cache (k text);",
		"/* DROP TABLE users; */ CREATE TABLE a (id int); CREATE TABLE b (id int); CREATE TABLE sprint_log (id int);",
		"DROP TABLE IF EXISTS a, public.b CASCADE; ALTER TABLE sprint_log RENAME TO sprint_events;",
	)
	want := []string{"cache", "sprint_events", "users"}
	if fmt.Sprint(sortedStrings(got)) != fmt.Sprint(want) {
		t.Fatalf("schemaTables = %v, want %v", sortedStrings(got), want)
	}
}

func TestTablesIn(t *testing.T) {
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT 1 FROM tasks t JOIN users u ON u.id = t.assignee_id", []string{"tasks", "users"}},
		{"SELECT 1 FROM users u, projects p WHERE true", []string{"users", "projects"}},
		{"SELECT 1 FROM users AS u , public.projects p,sprints", []string{"users", "projects", "sprints"}},
		{`DELETE FROM "tasks" WHERE true`, []string{"tasks"}},
		{"INSERT INTO tasks(id) VALUES (1)", []string{"tasks"}},
		{"UPDATE tasks SET a = 1, b = 2 FROM users WHERE true", []string{"tasks", "users"}},
		{"TRUNCATE TABLE worklogs, tasks; LOCK TABLE sprints IN SHARE MODE", []string{"worklogs", "tasks", "sprints"}},
		{"SELECT EXTRACT(EPOCH FROM tasks.created_at) FROM x", nil},
		{"WITH x AS (SELECT 1) SELECT * FROM x, unnest(?) u", nil},
		{"ON CONFLICT (id) DO UPDATE SET a = 1", nil},
		{"telegram: bot removed from group, projects unbound", nil},
		{"FROM users u JOIN projects p ON true", []string{"users", "projects"}},
	}
	for _, c := range cases {
		if got := tablesIn(c.sql); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("tablesIn(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// TestScanFindsViolation — сканер видит SQL в обычных и raw-строках и в .Table(...), не путает
// чужие таблицы со своими и считает каждое обращение.
func TestScanFindsViolation(t *testing.T) {
	root := t.TempDir()
	write := func(p, src string) { writeFixture(t, root, p, src) }
	write("telegram/card.go", "package telegram\nconst q = `SELECT t.id\n  FROM tasks t JOIN projects p ON p.id = t.project_id`\n"+
		"const q2 = \"UPDATE tasks SET x = 1\"\nconst own = \"DELETE FROM one_time_codes\"\nvar _ = db.Table(\"users u\")\n")
	write("task/store.go", "package task\nconst q = \"UPDATE tasks SET x = 1\"\n// FROM users в комментарии не считается\n")
	write("task/store_test.go", "package task\nconst q = \"SELECT 1 FROM users\"\n")
	write("report/report.go", "package report\nconst q = \"SELECT 1 FROM tasks JOIN sprints s ON true\"\n")
	write("testutil/fixtures.go", "package testutil\nconst q = \"DELETE FROM tasks\"\n")
	write("events/outbox.go", "package events\nconst q = \"INSERT INTO outbox_events (id) VALUES (1)\"\n")
	write("me/me.go", "package me\nconst q = \"INSERT INTO users (id) VALUES (1)\"\nconst cte = \"WITH x AS (SELECT 1) SELECT * FROM x\"\n")

	got := scan(t, root)
	want := map[violation]int{
		{"telegram/card.go", "tasks"}:    2,
		{"telegram/card.go", "projects"}: 1,
		{"telegram/card.go", "users"}:    1,
		{"me/me.go", "users"}:            1,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

// scan считает обращения к чужим таблицам в строковых литералах и в вызовах .Table("…") не-тестовых
// файлов пакетов под root.
func scan(t *testing.T, root string) map[violation]int {
	t.Helper()
	found := map[violation]int{}
	for _, f := range goFiles(t, root) {
		module := f.module
		if readAnyTable[module] || notModules[module] {
			continue
		}
		if m, ok := moduleOf[module]; ok {
			module = m
		}
		add := func(table string) {
			if owner := owners[table]; owner != "" && owner != module {
				found[violation{f.rel, table}]++
			}
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if table, ok := tableCall(n); ok {
					add(table)
				}
			case *ast.BasicLit:
				if s, ok := stringLit(n); ok {
					for _, table := range tablesIn(s) {
						add(table)
					}
				}
			}
			return true
		})
	}
	return found
}

// tableCall — db.Table("tasks t"): имя таблицы без ключевого слова SQL.
func tableCall(c *ast.CallExpr) (string, bool) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Table" || len(c.Args) == 0 {
		return "", false
	}
	s, ok := stringLit(c.Args[0])
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(strings.TrimSpace(s), " ")
	return strings.ToLower(strings.Trim(strings.TrimPrefix(name, "public."), `"`)), true
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// readViolations читает список временных нарушений: строки «файл таблица число_обращений».
func readViolations(t *testing.T, path string) map[violation]int {
	t.Helper()
	out := map[violation]int{}
	for key, n := range readList(t, path, 3) {
		file, table, _ := strings.Cut(key, " ")
		out[violation{file, table}] = n
	}
	return out
}

func sortedKeys(m map[violation]int) []violation {
	out := make([]violation, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].table < out[j].table
	})
	return out
}

func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
