package archtest

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
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
}

// readAnyTable — постоянные исключения: модули, которым разрешено читать и писать любые
// таблицы. report — агрегирующие отчёты одним запросом ради производительности (ТЗ §10.3),
// loadgen — генератор тестовых данных. Новое исключение — только правкой этого списка
// и api/CLAUDE.md.
var readAnyTable = map[string]bool{
	"report":  true,
	"loadgen": true,
}

// sqlTable находит таблицу после ключевого слова SQL: FROM (в том числе DELETE FROM),
// JOIN, UPDATE, INTO. Имена, которых нет в owners (CTE, функции, алиасы), игнорируются.
var sqlTable = regexp.MustCompile(`(?i)\b(?:from|join|update|into)\s+([a-z_][a-z0-9_]*)`)

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
		if allowed[v] {
			continue
		}
		owner := owners[v.table]
		msgs = append(msgs, fmt.Sprintf("%s обращается к %s (владелец %s) — вызовите функцию модуля %s", v.file, v.table, owner, owner))
	}
	for _, v := range sortedKeys(allowed) {
		if !found[v] {
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
	create := regexp.MustCompile(`(?i)create\s+table\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)`)
	drop := regexp.MustCompile(`(?i)drop\s+table\s+(?:if\s+exists\s+)?([a-z_][a-z0-9_]*)`)
	tables := map[string]bool{}
	sort.Strings(files) // имена начинаются с времени — порядок применения
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range create.FindAllStringSubmatch(string(b), -1) {
			tables[strings.ToLower(m[1])] = true
		}
		for _, m := range drop.FindAllStringSubmatch(string(b), -1) {
			delete(tables, strings.ToLower(m[1]))
		}
	}
	for table := range tables {
		if _, ok := owners[table]; !ok {
			t.Errorf("таблица %s без владельца — добавьте её в owners и в api/CLAUDE.md", table)
		}
	}
	for table := range owners {
		if !tables[table] {
			t.Errorf("таблицы %s нет в миграциях — удалите её из owners", table)
		}
	}
}

// TestScanFindsViolation — сканер видит SQL в обычных и raw-строках и не путает
// чужие таблицы со своими.
func TestScanFindsViolation(t *testing.T) {
	root := t.TempDir()
	write := func(path, src string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("telegram/card.go", "package telegram\nconst q = `SELECT t.id\n  FROM tasks t JOIN projects p ON p.id = t.project_id`\nconst own = \"DELETE FROM one_time_codes\"\n")
	write("task/store.go", "package task\nconst q = \"UPDATE tasks SET x = 1\"\n// FROM users в комментарии не считается\n")
	write("task/store_test.go", "package task\nconst q = \"SELECT 1 FROM users\"\n")
	write("report/report.go", "package report\nconst q = \"SELECT 1 FROM tasks JOIN sprints s ON true\"\n")
	write("me/me.go", "package me\nconst q = \"INSERT INTO users (id) VALUES (1)\"\nconst cte = \"WITH x AS (SELECT 1) SELECT * FROM x\"\n")

	got := scan(t, root)
	want := map[violation]bool{
		{"telegram/card.go", "tasks"}:    true,
		{"telegram/card.go", "projects"}: true,
		{"me/me.go", "users"}:            true,
	}
	if fmt.Sprint(sortedKeys(got)) != fmt.Sprint(sortedKeys(want)) {
		t.Fatalf("scan = %v, want %v", sortedKeys(got), sortedKeys(want))
	}
}

// scan обходит Go-файлы пакетов под root (кроме тестов) и возвращает обращения
// к чужим таблицам в строковых литералах.
func scan(t *testing.T, root string) map[violation]bool {
	t.Helper()
	found := map[violation]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		module, _, _ := strings.Cut(rel, "/")
		if readAnyTable[module] {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, m := range sqlTable.FindAllStringSubmatch(s, -1) {
				table := strings.ToLower(m[1])
				if owner, ok := owners[table]; ok && owner != module {
					found[violation{rel, table}] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// readViolations читает список временных нарушений: строки «файл таблица»,
// пустые строки и комментарии # пропускаются.
func readViolations(t *testing.T, path string) map[violation]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[violation]bool{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("%s:%d: ожидается «файл таблица», получено %q", path, n, line)
		}
		v := violation{fields[0], fields[1]}
		if out[v] {
			t.Fatalf("%s:%d: повтор %q", path, n, line)
		}
		out[v] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sortedKeys(m map[violation]bool) []violation {
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
