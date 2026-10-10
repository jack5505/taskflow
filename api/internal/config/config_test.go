package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_ACCESS_TTL", "")
	t.Setenv("JWT_REFRESH_TTL", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.Addr)
	require.Equal(t, "localhost:6379", cfg.RedisAddr)
	require.Equal(t, 15*time.Minute, cfg.JWTAccessTTL)
	require.Equal(t, 30*24*time.Hour, cfg.JWTRefreshTTL)
	require.EqualValues(t, 50<<20, cfg.AttachmentMaxBytes)
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	_, err := config.Load()
	require.ErrorContains(t, err, "DATABASE_URL")
}

func TestLoadRejectsBadDurations(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	for _, key := range []string{"JWT_ACCESS_TTL", "JWT_REFRESH_TTL"} {
		t.Setenv("JWT_ACCESS_TTL", "15m")
		t.Setenv("JWT_REFRESH_TTL", "720h")
		t.Setenv(key, "скоро")
		_, err := config.Load()
		require.ErrorContains(t, err, key)
	}
}

func TestLoadReadsOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("APP_ADDR", ":9999")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("JWT_ACCESS_TTL", "5m")
	t.Setenv("SMTP_FROM", "TaskFlow <noreply@example.com>")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, ":9999", cfg.Addr)
	require.Equal(t, "redis:6379", cfg.RedisAddr)
	require.Equal(t, 5*time.Minute, cfg.JWTAccessTTL)
	require.Equal(t, "TaskFlow <noreply@example.com>", cfg.SMTPFrom)
}
