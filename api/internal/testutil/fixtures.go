package testutil

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

var seq atomic.Int64

// Next — следующий номер общего счётчика: для уникальных числовых значений (telegram_user_id и т. п.).
func Next() int64 { return seq.Add(1) }

// UniqueEmail — email, не совпадающий с другими тестами и повторами (-count): база у пакета общая.
func UniqueEmail(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(t.Name()))
	return fmt.Sprintf("%s-%d@example.com", name, seq.Add(1))
}

// UniqueKey — ключ проекта по правилу ^[A-Z]{2,10}$, уникальный в пределах процесса.
func UniqueKey(t *testing.T) string {
	t.Helper()
	n := seq.Add(1)
	key := make([]byte, 0, 6)
	key = append(key, 'T')
	for n > 0 || len(key) < 2 {
		key = append(key, byte('A'+n%26))
		n /= 26
	}
	return string(key)
}
