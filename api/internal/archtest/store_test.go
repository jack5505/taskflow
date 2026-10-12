package archtest

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Внутри модуля SQL пишется только в слое запросов (api/CLAUDE.md, «Внутри модуля два слоя»):
// файлы store.go и store_*.go или пакеты запросов, вынесенные ради циклов импортов.
var (
	storeFile     = regexp.MustCompile(`^store(_[a-z0-9_]+)?\.go$`)
	storePackages = map[string]bool{"sprint/sprintdb": true, "task/taskdb": true}
	// sqlStatement — строка начинается с оператора SQL (запрос целиком, а не фрагмент Where).
	sqlStatement = regexp.MustCompile(`(?i)^\s*(select\s|insert\s+into\s|update\s+\S+\s+set\s|delete\s+from\s|with\s+\w+\s+as\s|truncate\s|lock\s+table\s)`)
)

const storeViolationsFile = "temporary_sql_outside_store.txt"

func TestSQLOnlyInStore(t *testing.T) {
	found := scanOutsideStore(t, "..")
	allowed := readCounts(t, storeViolationsFile)

	var msgs []string
	for _, f := range sortedStrings(found) {
		switch n, ok := allowed[f]; {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("%s: SQL вне слоя запросов (%d) — перенесите запросы в store.go модуля", f, found[f]))
		case found[f] > n:
			msgs = append(msgs, fmt.Sprintf("%s: новый SQL вне слоя запросов (было %d, стало %d) — перенесите в store.go", f, n, found[f]))
		case found[f] < n:
			msgs = append(msgs, fmt.Sprintf("%s: SQL вне store стало меньше (%d вместо %d) — исправьте число в %s", f, found[f], n, storeViolationsFile))
		}
	}
	for _, f := range sortedStrings(allowed) {
		if found[f] == 0 {
			msgs = append(msgs, fmt.Sprintf("%s: SQL вне store не осталось — удалите строку из %s", f, storeViolationsFile))
		}
	}
	if len(msgs) > 0 {
		t.Fatalf("SQL только в слое запросов (api/CLAUDE.md):\n%s", strings.Join(msgs, "\n"))
	}
}

// TestScanOutsideStore — store-файлы и пакеты запросов не считаются, сервисные — считаются;
// фрагменты условий GORM («deleted_at IS NULL») — не SQL-запросы.
func TestScanOutsideStore(t *testing.T) {
	root := t.TempDir()
	write := func(p, src string) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("task/service.go", "package task\nconst a = `SELECT 1 FROM tasks`\nconst b = \"deleted_at IS NULL\"\nconst c = \"UPDATE tasks SET x = 1\"\n")
	write("task/store.go", "package task\nconst a = `SELECT 1 FROM tasks`\n")
	write("task/store_timer.go", "package task\nconst a = `DELETE FROM active_timers`\n")
	write("task/taskdb/taskdb.go", "package taskdb\nconst a = `SELECT 1 FROM tasks`\n")
	write("task/service_test.go", "package task\nconst a = `SELECT 1 FROM tasks`\n")
	write("report/report.go", "package report\nconst a = `SELECT 1 FROM tasks`\n")
	write("me/me.go", "package me\nconst a = `SELECT 1`\n")
	write("telegram/sender.go", "package telegram\nconst OpDelete = \"delete\"\nconst q = \"with x as (select 1) select * from x\"\n")
	write("events/outbox.go", "package events\nconst a = `INSERT INTO outbox_events (id) VALUES (1)`\n")

	got := scanOutsideStore(t, root)
	want := map[string]int{"task/service.go": 2, "events/outbox.go": 1, "telegram/sender.go": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("scanOutsideStore = %v, want %v", got, want)
	}
}

// scanOutsideStore — число SQL-строк в не-тестовых файлах модулей-владельцев таблиц вне слоя запросов.
func scanOutsideStore(t *testing.T, root string) map[string]int {
	t.Helper()
	modules := map[string]bool{}
	for _, m := range owners {
		modules[m] = true
	}
	for pkg := range moduleOf {
		modules[pkg] = true
	}
	found := map[string]int{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		module, _, _ := strings.Cut(rel, "/")
		if !modules[module] || storeFile.MatchString(path.Base(rel)) || storePackages[path.Dir(rel)] {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := stringLit(asExpr(n)); ok && (sqlStatement.MatchString(s) || len(tablesIn(s)) > 0) {
				found[rel]++
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

func asExpr(n ast.Node) ast.Expr {
	e, _ := n.(ast.Expr)
	return e
}

// readCounts читает список «файл число»; пустые строки и комментарии # пропускаются.
func readCounts(t *testing.T, name string) map[string]int {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]int{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("%s:%d: ожидается «файл число», получено %q", name, n, line)
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil || count < 1 {
			t.Fatalf("%s:%d: число должно быть положительным, получено %q", name, n, fields[1])
		}
		if _, dup := out[fields[0]]; dup {
			t.Fatalf("%s:%d: повтор %q", name, n, line)
		}
		out[fields[0]] = count
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
