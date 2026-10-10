package testutil

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// LogBuffer — потокобезопасный приёмник логов: в него пишут горутины сервера, читает тест.
type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String возвращает всё, что записано к этому моменту.
func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Contains сообщает, есть ли подстрока в логе.
func (b *LogBuffer) Contains(s string) bool { return strings.Contains(b.String(), s) }

// Count считает строки лога с подстрокой.
func (b *LogBuffer) Count(s string) int {
	n := 0
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.Contains(line, s) {
			n++
		}
	}
	return n
}

// CaptureLogs направляет slog.Default() в буфер на время теста (уровень Debug).
func CaptureLogs(t *testing.T) *LogBuffer {
	t.Helper()
	b := &LogBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return b
}
