package config_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", testSecret)
	for _, key := range []string{"APP_ADDR", "REDIS_ADDR", "JWT_ACCESS_TTL", "JWT_REFRESH_TTL", "ATTACHMENT_MAX_BYTES"} {
		t.Setenv(key, "") // пустое значение = не задано; иначе тест зависел бы от .env в оболочке
	}

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
	t.Setenv("JWT_SECRET", testSecret)
	_, err := config.Load()
	require.ErrorContains(t, err, "DATABASE_URL")
}

func TestLoadRequiresStrongJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	for _, secret := range []string{"", "short"} {
		t.Setenv("JWT_SECRET", secret)
		_, err := config.Load()
		require.ErrorContains(t, err, "JWT_SECRET")
	}
}

func TestLoadRejectsBadDurations(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", testSecret)
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
	t.Setenv("JWT_SECRET", testSecret)
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

func TestLoadAttachmentMaxBytes(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", testSecret)
	t.Setenv("ATTACHMENT_MAX_BYTES", "1048576")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.EqualValues(t, 1<<20, cfg.AttachmentMaxBytes)
	for _, bad := range []string{"много", "0", "-1"} {
		t.Setenv("ATTACHMENT_MAX_BYTES", bad)
		_, err := config.Load()
		require.ErrorContains(t, err, "ATTACHMENT_MAX_BYTES", bad)
	}
}

// Бот выключен без токена: настройки бота не проверяются. С токеном webhook требует секрет
// (§8.8), long polling запрещён в production.
func TestLoadTelegram(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", testSecret)
	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_WEBHOOK_SECRET", "TELEGRAM_BOT_USERNAME", "TELEGRAM_POLLING", "TELEGRAM_API_URL", "APP_ENV"} {
		t.Setenv(key, "")
	}
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "change-me")
	cfg, err := config.Load()
	require.NoError(t, err, "без токена бот выключен, заглушка секрета не мешает")
	require.False(t, cfg.TelegramEnabled())
	require.Equal(t, "https://api.telegram.org", cfg.TelegramAPIURL)

	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "Abc_123-xyz")
	_, err = config.Load()
	require.ErrorContains(t, err, "TELEGRAM_BOT_USERNAME", "без имени бота не разобрать /cmd@бот")
	t.Setenv("TELEGRAM_BOT_USERNAME", "taskflow_bot")
	for _, s := range []string{"", "change-me", "с пробелом и кириллицей"} {
		t.Setenv("TELEGRAM_WEBHOOK_SECRET", s)
		_, err = config.Load()
		require.ErrorContains(t, err, "TELEGRAM_WEBHOOK_SECRET", s)
	}
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "Abc_123-xyz")
	cfg, err = config.Load()
	require.NoError(t, err)
	require.True(t, cfg.TelegramEnabled())
	require.True(t, cfg.TelegramWebhook())

	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "")
	t.Setenv("TELEGRAM_POLLING", "true")
	cfg, err = config.Load()
	require.NoError(t, err, "в разработке long polling без секрета допустим")
	require.True(t, cfg.TelegramPolling)
	require.False(t, cfg.TelegramWebhook())

	t.Setenv("APP_ENV", "production")
	_, err = config.Load()
	require.ErrorContains(t, err, "TELEGRAM_POLLING")

	t.Setenv("TELEGRAM_POLLING", "иногда")
	_, err = config.Load()
	require.ErrorContains(t, err, "TELEGRAM_POLLING")
}

// §12.1: интеграция с GitHub выключена без GITHUB_APP_ID; с ним нужны ключ App и секрет вебхуков.
func TestLoadGitHub(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", testSecret)
	set := func(id, key, secret, client, clientSecret string) {
		t.Setenv("GITHUB_APP_ID", id)
		t.Setenv("GITHUB_APP_PRIVATE_KEY_FILE", key)
		t.Setenv("GITHUB_WEBHOOK_SECRET", secret)
		t.Setenv("GITHUB_CLIENT_ID", client)
		t.Setenv("GITHUB_CLIENT_SECRET", clientSecret)
		t.Setenv("GITHUB_API_URL", "")
	}
	set("", "", "", "", "")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.False(t, cfg.GitHubEnabled())
	require.Equal(t, "https://api.github.com", cfg.GitHubAPIURL)

	key := writeKey(t)
	set("12345", key, "0123456789abcdef", "Iv1.abc", "s3cr3t")
	cfg, err = config.Load()
	require.NoError(t, err)
	require.True(t, cfg.GitHubEnabled())

	for name, args := range map[string][5]string{
		"id не число":           {"app", key, "0123456789abcdef", "", ""},
		"id ноль":               {"0", key, "0123456789abcdef", "", ""},
		"нет ключа":             {"12345", "", "0123456789abcdef", "", ""},
		"нет файла ключа":       {"12345", key + ".missing", "0123456789abcdef", "", ""},
		"не PEM":                {"12345", notPEM(t), "0123456789abcdef", "", ""},
		"короткий секрет":       {"12345", key, "short", "", ""},
		"секрет по умолчанию":   {"12345", key, "change-me", "", ""},
		"client id без секрета": {"12345", key, "0123456789abcdef", "Iv1.abc", ""},
	} {
		set(args[0], args[1], args[2], args[3], args[4])
		_, err := config.Load()
		require.Error(t, err, name)
	}
}

func writeKey(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}), 0o600))
	return p
}

func notPEM(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bad.pem")
	require.NoError(t, os.WriteFile(p, []byte("not a key"), 0o600))
	return p
}
