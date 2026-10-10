package db_test

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/migrations"
)

// latestVersion — номер самой свежей миграции в migrations.FS, чтобы тест не ломался
// при добавлении новых файлов.
func latestVersion(t *testing.T) uint {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	var latest uint64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		v, err := strconv.ParseUint(name[:strings.Index(name, "_")], 10, 64)
		require.NoError(t, err, name)
		if v > latest {
			latest = v
		}
	}
	require.NotZero(t, latest, "в migrations нет ни одной up-миграции")
	return uint(latest)
}

func TestMigrateUpAndDown(t *testing.T) {
	dsn := testutil.FreshDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	g, err := db.Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(g) })

	_, ok, err := db.Version(ctx, dsn, migrations.FS)
	require.NoError(t, err)
	require.False(t, ok, "на пустой базе версии нет")

	require.NoError(t, db.Migrate(ctx, dsn, migrations.FS))

	v, ok, err := db.Version(ctx, dsn, migrations.FS)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, latestVersion(t), v)

	// Повторный запуск идемпотентен (ErrNoChange не считается ошибкой).
	require.NoError(t, db.Migrate(ctx, dsn, migrations.FS))

	var n int64
	require.NoError(t, g.Raw("SELECT count(*) FROM pg_extension WHERE extname = 'citext'").Scan(&n).Error)
	require.Equal(t, int64(1), n, "миграция init должна включить citext")

	require.NoError(t, db.MigrateDown(ctx, dsn, migrations.FS))

	_, ok, err = db.Version(ctx, dsn, migrations.FS)
	require.NoError(t, err)
	require.False(t, ok, "после down версии быть не должно")

	require.NoError(t, g.Raw("SELECT count(*) FROM pg_extension WHERE extname = 'citext'").Scan(&n).Error)
	require.Equal(t, int64(0), n)
}

func TestSharedDBIsMigrated(t *testing.T) {
	dsn := testutil.SharedDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v, ok, err := db.Version(ctx, dsn, migrations.FS)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, latestVersion(t), v)
}

func TestOpenHonoursContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	// 10.255.255.1 — немаршрутизируемый адрес: пакеты уходят в никуда, TCP-таймаут ОС намного больше 2 с.
	_, err := db.Open(ctx, "postgres://u:p@10.255.255.1:5432/x?sslmode=disable")
	require.Error(t, err)
	require.Less(t, time.Since(start), 10*time.Second, "Open должен прерываться по контексту")
}
