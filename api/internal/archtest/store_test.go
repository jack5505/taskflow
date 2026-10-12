package archtest

import (
	"fmt"
	"go/ast"
	"path"
	"regexp"
	"strings"
	"testing"
)

// Внутри модуля запросы к базе пишутся только в слое запросов (api/CLAUDE.md, «Внутри модуля
// два слоя»): файлы store.go и store_*.go или пакеты запросов, вынесенные ради циклов импортов.
var (
	storeFile = regexp.MustCompile(`^store(_[a-z0-9_]+)?\.go$`)
	// storePackages — пакеты запросов целиком (и их подпакеты).
	storePackages = []string{"sprint/sprintdb", "task/taskdb"}
	// sqlStatement — строка — запрос целиком: начинается с оператора SQL (после комментариев
	// «--» и открывающей скобки). Тексты ошибок вроде «update tasks: %w» запросом не считаются.
	sqlStatement = regexp.MustCompile(`(?is)^\s*(?:--[^\n]*\n\s*)*\(?\s*(?:select\s|insert\s+into\s|update\s+\S+\s+(?:\S+\s+)?set\s|delete\s+from\s|with\s+(?:recursive\s+)?\w+\s*(?:\([^)]*\))?\s*as\s*\(|truncate\s|lock\s+table\s)`)
	// readBuilders — методы GORM, которые выполняют запрос чтения, собранный цепочкой
	// (Model/Where/Select…): такой запрос тоже место слоя запросов. Take не считается: так
	// называется и операция «взять задачу» сервиса task.
	readBuilders = map[string]bool{"Pluck": true, "Find": true, "First": true}
	// domainServices — пакеты без своих таблиц, которые тоже не пишут запросов сами.
	domainServices = map[string]bool{"me": true, "users": true}
)

const storeViolationsFile = "temporary_sql_outside_store.txt"

func TestSQLOnlyInStore(t *testing.T) {
	found := scanOutsideStore(t, "..")
	allowed := readList(t, storeViolationsFile, 2)

	var msgs []string
	for _, f := range sortedStrings(found) {
		switch n, ok := allowed[f]; {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("%s: запросы вне слоя запросов (%d) — перенесите в store.go модуля", f, found[f]))
		case found[f] > n:
			msgs = append(msgs, fmt.Sprintf("%s: новый запрос вне слоя запросов (было %d, стало %d) — перенесите в store.go", f, n, found[f]))
		case found[f] < n:
			msgs = append(msgs, fmt.Sprintf("%s: запросов вне store стало меньше (%d вместо %d) — исправьте число в %s", f, found[f], n, storeViolationsFile))
		}
	}
	for _, f := range sortedStrings(allowed) {
		if found[f] == 0 {
			msgs = append(msgs, fmt.Sprintf("%s: запросов вне store не осталось — удалите строку из %s", f, storeViolationsFile))
		}
	}
	if len(msgs) > 0 {
		t.Fatalf("запросы только в слое запросов (api/CLAUDE.md):\n%s", strings.Join(msgs, "\n"))
	}
}

func TestSQLStatement(t *testing.T) {
	for s, want := range map[string]bool{
		"SELECT 1 FROM tasks":                      true,
		"  insert into tasks (id) values (1)":      true,
		"UPDATE tasks t SET x = 1":                 true,
		"UPDATE tasks SET x = 1":                   true,
		"DELETE FROM tasks":                        true,
		"WITH RECURSIVE d AS (SELECT 1) SELECT *":  true,
		"with x(a) as(select 1) select a from x":   true,
		"(SELECT 1) UNION (SELECT 2)":              true,
		"-- lock\nSELECT pg_advisory_xact_lock(?)": true,
		"TRUNCATE tasks":                           true,
		"update tasks: %w":                         false,
		"delete":                                   false,
		"select a project":                         true, // слово и пробел — как запрос; такие тексты не пишем
		"deleted_at IS NULL":                       false,
		"failed to insert into worklogs":           false,
	} {
		if got := sqlStatement.MatchString(s); got != want {
			t.Errorf("sqlStatement(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestScanOutsideStore — store-файлы и пакеты запросов не считаются, сервисные — считаются;
// фрагменты условий GORM и тексты ошибок — не запросы.
func TestScanOutsideStore(t *testing.T) {
	root := t.TempDir()
	write := func(p, src string) { writeFixture(t, root, p, src) }
	write("task/service.go", "package task\nconst a = `SELECT 1 FROM tasks`\nconst b = \"deleted_at IS NULL\"\n"+
		"const c = \"UPDATE tasks SET x = 1\"\nvar e = fmt.Errorf(\"update tasks: %w\", err)\n"+
		"func f() { db.Table(\"tasks\").Where(\"x\").Updates(m); db.Model(&Task{}).Where(\"y\").Pluck(\"id\", &ids); db.First(&t) }\n")
	write("task/store.go", "package task\nconst a = `SELECT 1 FROM tasks`\nfunc f() { db.Find(&x) }\n")
	write("task/store_timer.go", "package task\nconst a = `DELETE FROM active_timers`\n")
	write("task/taskdb/taskdb.go", "package taskdb\nconst a = `SELECT 1 FROM tasks`\n")
	write("task/taskdb/inner/q.go", "package inner\nconst a = `SELECT 1 FROM tasks`\n")
	write("task/service_test.go", "package task\nconst a = `SELECT 1 FROM tasks`\n")
	write("report/report.go", "package report\nconst a = `SELECT 1 FROM tasks`\n")
	write("me/me.go", "package me\nconst a = `SELECT now()`\n")
	write("worker/worker.go", "package worker\nconst a = `SELECT 1`\n")
	write("events/outbox.go", "package events\nconst a = `INSERT INTO outbox_events (id) VALUES (1)`\n")
	write("telegram/sender.go", "package telegram\nconst OpDelete = \"delete\"\nconst q = \"with x as (select 1) select * from x\"\n")

	got := scanOutsideStore(t, root)
	want := map[string]int{"task/service.go": 5, "events/outbox.go": 1, "telegram/sender.go": 1, "me/me.go": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("scanOutsideStore = %v, want %v", got, want)
	}
}

// scanOutsideStore — число запросов (строки SQL, вызовы .Table и чтения цепочкой GORM) в
// не-тестовых файлах модулей-владельцев таблиц и доменных сервисов вне слоя запросов.
func scanOutsideStore(t *testing.T, root string) map[string]int {
	t.Helper()
	modules := map[string]bool{}
	for _, m := range owners {
		modules[m] = true
	}
	for pkg := range moduleOf {
		modules[pkg] = true
	}
	for pkg := range domainServices {
		modules[pkg] = true
	}
	found := map[string]int{}
	for _, f := range goFiles(t, root) {
		if !modules[f.module] || storeFile.MatchString(path.Base(f.rel)) || inStorePackage(f.rel) {
			continue
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if _, ok := tableCall(n); ok {
					found[f.rel]++
				} else if sel, ok := n.Fun.(*ast.SelectorExpr); ok && readBuilders[sel.Sel.Name] {
					found[f.rel]++
				}
			case *ast.BasicLit:
				if s, ok := stringLit(n); ok && sqlStatement.MatchString(s) {
					found[f.rel]++
				}
			}
			return true
		})
	}
	return found
}

func inStorePackage(rel string) bool {
	for _, p := range storePackages {
		if strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}
