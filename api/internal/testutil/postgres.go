// Package testutil — помощники для интеграционных тестов на настоящем PostgreSQL (CONTRIBUTING §7).
package testutil

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/migrations"
)

var (
	sharedOnce sync.Once
	sharedDSN  string
	sharedErr  error
)

// SharedDSN поднимает один контейнер PostgreSQL на тестовый пакет и применяет миграции.
// Контейнер убирает Ryuk по завершении процесса `go test`. В режиме -short тест пропускается.
func SharedDSN(t *testing.T) string {
	t.Helper()
	skipIfShort(t)

	sharedOnce.Do(func() {
		sharedDSN, sharedErr = startContainer(context.Background())
		if sharedErr != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		sharedErr = db.Migrate(ctx, sharedDSN, migrations.FS)
	})
	if sharedErr != nil {
		t.Fatalf("shared postgres: %v", sharedErr)
	}
	return sharedDSN
}

// SharedDB возвращает пул GORM к общей базе пакета с применёнными миграциями.
func SharedDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := openDB(SharedDSN(t))
	if err != nil {
		t.Fatalf("open shared db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(g) })
	return g
}

// FreshDSN поднимает отдельный пустой контейнер без миграций — для тестов самих миграций.
func FreshDSN(t *testing.T) string {
	t.Helper()
	skipIfShort(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dsn, err := startContainer(ctx)
	if err != nil {
		t.Fatalf("postgres container: %v", err)
	}
	return dsn
}

func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("интеграционный тест: пропущен в -short")
	}
}

func startContainer(ctx context.Context) (string, error) {
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("taskflow_test"),
		tcpostgres.WithUsername("taskflow"),
		tcpostgres.WithPassword("taskflow"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", err
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable", "connect_timeout=5")
	if err != nil {
		return "", fmt.Errorf("postgres dsn: %w", err)
	}
	return dsn, nil
}

func openDB(dsn string) (*gorm.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return db.Open(ctx, dsn)
}
