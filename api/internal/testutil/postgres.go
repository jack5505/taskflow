// Package testutil — помощники для интеграционных тестов на настоящем PostgreSQL (CONTRIBUTING §7).
package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartPostgres поднимает контейнер postgres:16-alpine и возвращает DSN.
// Контейнер останавливается по завершении теста. В режиме -short тест пропускается.
func StartPostgres(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("интеграционный тест: пропущен в -short")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("taskflow_test"),
		tcpostgres.WithUsername("taskflow"),
		tcpostgres.WithPassword("taskflow"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Logf("terminate postgres: %v", err)
		}
	})

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}
	return dsn
}
