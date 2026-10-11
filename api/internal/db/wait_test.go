package db_test

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/migrations"
)

func TestLatestVersion(t *testing.T) {
	v, err := db.LatestVersion(migrations.FS)
	require.NoError(t, err)
	require.Equal(t, latestVersion(t), v)

	_, err = db.LatestVersion(fstest.MapFS{})
	require.ErrorContains(t, err, "migrations source")
}

// Воркер на базе без миграций ждёт и стартует, как только api их применит (issue #137).
func TestWaitForMigrationsWaitsUntilAPIMigrates(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	dsn := testutil.FreshDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	g, err := db.Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(g) })

	done := make(chan error, 1)
	go func() { done <- db.WaitForMigrations(ctx, g, migrations.FS, 50*time.Millisecond) }()

	select {
	case err := <-done:
		t.Fatalf("ожидание завершилось до миграций: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	require.True(t, logs.Contains("db: waiting for migrations"), "ожидание видно в логе")

	require.NoError(t, db.Migrate(ctx, dsn, migrations.FS))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("воркер не дождался применённых миграций")
	}
	require.True(t, logs.Contains("db: migrations ready"))

	// Схема уже актуальна — без ожидания и без записей в логе.
	before := logs.Count("db: waiting for migrations")
	require.NoError(t, db.WaitForMigrations(ctx, g, migrations.FS, time.Hour))
	require.Equal(t, before, logs.Count("db: waiting for migrations"))
}

// Старая схема или «грязная» версия после упавшей миграции: ждём до таймаута и выходим с ошибкой.
func TestWaitForMigrationsTimesOut(t *testing.T) {
	g := testutil.SharedDB(t)
	newer := fstest.MapFS{
		"1_a.up.sql":                   {Data: []byte("SELECT 1;")},
		"99999999999999_future.up.sql": {Data: []byte("SELECT 1;")},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := db.WaitForMigrations(ctx, g, newer, 50*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "want 99999999999999")

	// Схема новее бинарника (откат образа) — не ждём.
	older := fstest.MapFS{"1_a.up.sql": {Data: []byte("SELECT 1;")}}
	require.NoError(t, db.WaitForMigrations(context.Background(), g, older, time.Hour))
}

func TestWaitForMigrationsDirtySchema(t *testing.T) {
	g, _ := testutil.TempDB(t)
	require.NoError(t, g.Exec(`UPDATE schema_migrations SET dirty = true`).Error)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := db.WaitForMigrations(ctx, g, migrations.FS, 50*time.Millisecond)
	require.ErrorContains(t, err, "dirty=true")
}

func TestWaitForMigrationsReportsDatabaseError(t *testing.T) {
	g, _ := testutil.TempDB(t)
	require.NoError(t, db.Close(g))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := db.WaitForMigrations(ctx, g, migrations.FS, 50*time.Millisecond)
	require.ErrorContains(t, err, "schema version")
}
