package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/migrations"
)

func TestMigrateUpAndDown(t *testing.T) {
	dsn := testutil.StartPostgres(t)

	_, ok, err := db.Version(dsn, migrations.FS)
	require.NoError(t, err)
	require.False(t, ok, "на пустой базе версии нет")

	require.NoError(t, db.Migrate(dsn, migrations.FS))

	v, ok, err := db.Version(dsn, migrations.FS)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint(20261010000001), v)

	// Повторный запуск идемпотентен (ErrNoChange не считается ошибкой).
	require.NoError(t, db.Migrate(dsn, migrations.FS))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	g, err := db.Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(g) })

	var n int64
	require.NoError(t, g.Raw("SELECT count(*) FROM pg_extension WHERE extname = 'citext'").Scan(&n).Error)
	require.Equal(t, int64(1), n, "миграция init должна включить citext")

	require.NoError(t, db.MigrateDown(dsn, migrations.FS))

	_, ok, err = db.Version(dsn, migrations.FS)
	require.NoError(t, err)
	require.False(t, ok, "после down версии быть не должно")

	require.NoError(t, g.Raw("SELECT count(*) FROM pg_extension WHERE extname = 'citext'").Scan(&n).Error)
	require.Equal(t, int64(0), n)
}

func TestOpenRejectsEmptyURL(t *testing.T) {
	_, err := db.Open(context.Background(), "")
	require.Error(t, err)
}

func TestOpenFailsOnUnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := db.Open(ctx, "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	require.Error(t, err)
}
