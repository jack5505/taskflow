package archtest

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// goFile — разобранный не-тестовый Go-файл: путь от root и модуль (первый каталог).
type goFile struct {
	rel, module string
	file        *ast.File
}

// parsed — разобранные файлы по root: дерево читается один раз на запуск тестов пакета.
var parsed sync.Map

// goFiles — не-тестовые Go-файлы под root.
func goFiles(t *testing.T, root string) []goFile {
	t.Helper()
	if v, ok := parsed.Load(root); ok {
		return v.([]goFile)
	}
	var files []goFile
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
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		module, _, _ := strings.Cut(rel, "/")
		files = append(files, goFile{rel: rel, module: module, file: f})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed.Store(root, files)
	return files
}

// readList читает список-«храповик»: строки из fields полей, последнее — положительное число;
// ключ — остальные поля через пробел. Пустые строки и комментарии # пропускаются.
func readList(t *testing.T, name string, fields int) map[string]int {
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
		parts := strings.Fields(line)
		if len(parts) != fields {
			t.Fatalf("%s:%d: ожидается %d поля, получено %q", name, n, fields, line)
		}
		count, err := strconv.Atoi(parts[fields-1])
		if err != nil || count < 1 {
			t.Fatalf("%s:%d: число должно быть положительным, получено %q", name, n, parts[fields-1])
		}
		key := strings.Join(parts[:fields-1], " ")
		if _, dup := out[key]; dup {
			t.Fatalf("%s:%d: повтор %q", name, n, line)
		}
		out[key] = count
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// writeFixture пишет файл src по пути rel под root (синтетические пакеты в тестах сканеров).
func writeFixture(t *testing.T, root, rel, src string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}
