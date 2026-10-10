package db_test

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/db"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

func TestOpenRejectsMalformedURL(t *testing.T) {
	_, err := db.Open(context.Background(), "://не url")
	require.ErrorContains(t, err, "DATABASE_URL")
}

func TestPingAndCloseAfterClose(t *testing.T) {
	g := testutil.SharedDB(t)
	require.NoError(t, db.Ping(context.Background(), g))
	require.NoError(t, db.Close(g))
	require.Error(t, db.Ping(context.Background(), g), "закрытый пул не отвечает на ping")
	require.NoError(t, db.Close(g), "повторное закрытие не ошибка")
}

func TestMigrateFailsOnUnreachableDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	src := fstest.MapFS{"1_x.up.sql": {Data: []byte("SELECT 1;")}}
	require.Error(t, db.Migrate(ctx, "postgres://u:p@10.255.255.1:5432/x?sslmode=disable", src))
	require.Error(t, db.MigrateDown(ctx, "postgres://u:p@10.255.255.1:5432/x?sslmode=disable", src))
	_, _, err := db.Version(ctx, "postgres://u:p@10.255.255.1:5432/x?sslmode=disable", src)
	require.Error(t, err)
}

func TestMigrateFailsOnBrokenSQL(t *testing.T) {
	dsn := testutil.FreshDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src := fstest.MapFS{"1_broken.up.sql": {Data: []byte("CREATE TABLE (;")}}
	require.ErrorContains(t, db.Migrate(ctx, dsn, src), "migrate up")
}
