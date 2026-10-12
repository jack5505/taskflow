// Package config читает настройки из переменных окружения (ТЗ §10.3: секреты только в env).
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Config struct {
	Addr    string
	BaseURL string
	Env     string

	DatabaseURL string
	RedisAddr   string

	JWTSecret     string
	JWTAccessTTL  time.Duration
	JWTRefreshTTL time.Duration

	// Бот выключен, пока TELEGRAM_BOT_TOKEN пуст: API и воркер работают, webhook не регистрируется.
	TelegramBotToken      string
	TelegramWebhookSecret string
	TelegramBotUsername   string
	TelegramAPIURL        string // Bot API; в тестах — фейковый сервер
	TelegramPolling       bool   // long polling вместо webhook, только не в production (§8.8)

	// GitHub App (ТЗ §12). Интеграция выключена, пока GITHUB_APP_ID пуст.
	GitHubAppID          string
	GitHubPrivateKeyFile string // PEM приватного ключа App
	GitHubWebhookSecret  string
	GitHubClientID       string // авторизация пользователя App — связка аккаунтов (§12.5)
	GitHubClientSecret   string
	GitHubAPIURL         string // REST API; в тестах — фейковый сервер
	GitHubWebURL         string // github.com: вход пользователя (OAuth); в тестах — фейковый сервер

	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	AttachmentsDir     string
	AttachmentMaxBytes int64
}

func Load() (*Config, error) {
	c := &Config{
		Addr:                  getenv("APP_ADDR", ":8080"),
		BaseURL:               getenv("APP_BASE_URL", "http://localhost:8080"),
		Env:                   getenv("APP_ENV", "development"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		RedisAddr:             getenv("REDIS_ADDR", "localhost:6379"),
		JWTSecret:             os.Getenv("JWT_SECRET"),
		TelegramBotToken:      os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramWebhookSecret: os.Getenv("TELEGRAM_WEBHOOK_SECRET"),
		TelegramBotUsername:   os.Getenv("TELEGRAM_BOT_USERNAME"),
		TelegramAPIURL:        getenv("TELEGRAM_API_URL", "https://api.telegram.org"),
		GitHubAppID:           os.Getenv("GITHUB_APP_ID"),
		GitHubPrivateKeyFile:  os.Getenv("GITHUB_APP_PRIVATE_KEY_FILE"),
		GitHubWebhookSecret:   os.Getenv("GITHUB_WEBHOOK_SECRET"),
		GitHubClientID:        os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret:    os.Getenv("GITHUB_CLIENT_SECRET"),
		GitHubAPIURL:          getenv("GITHUB_API_URL", "https://api.github.com"),
		GitHubWebURL:          getenv("GITHUB_WEB_URL", "https://github.com"),
		SMTPHost:              os.Getenv("SMTP_HOST"),
		SMTPPort:              getenv("SMTP_PORT", "587"),
		SMTPUser:              os.Getenv("SMTP_USER"),
		SMTPPassword:          os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:              os.Getenv("SMTP_FROM"),
		AttachmentsDir:        getenv("ATTACHMENTS_DIR", "./data/attachments"),
	}

	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL не задан")
	}
	if len(c.JWTSecret) < 32 {
		return nil, errors.New("JWT_SECRET не задан или короче 32 символов")
	}

	var err error
	if c.JWTAccessTTL, err = time.ParseDuration(getenv("JWT_ACCESS_TTL", "15m")); err != nil {
		return nil, fmt.Errorf("JWT_ACCESS_TTL: %w", err)
	}
	if c.JWTRefreshTTL, err = time.ParseDuration(getenv("JWT_REFRESH_TTL", "720h")); err != nil {
		return nil, fmt.Errorf("JWT_REFRESH_TTL: %w", err)
	}
	// §3.9: до 50 МБ; переменная позволяет только уточнить лимит на стенде.
	if c.AttachmentMaxBytes, err = strconv.ParseInt(getenv("ATTACHMENT_MAX_BYTES", "52428800"), 10, 64); err != nil || c.AttachmentMaxBytes <= 0 {
		return nil, errors.New("ATTACHMENT_MAX_BYTES: нужно положительное число байт")
	}
	if c.TelegramPolling, err = strconv.ParseBool(getenv("TELEGRAM_POLLING", "false")); err != nil {
		return nil, errors.New("TELEGRAM_POLLING: ожидается true или false")
	}
	if err := c.checkTelegram(); err != nil {
		return nil, err
	}
	if err := c.checkGitHub(); err != nil {
		return nil, err
	}
	return c, nil
}

// GitHubEnabled — интеграция с GitHub включена: задан GITHUB_APP_ID (§12.1).
func (c *Config) GitHubEnabled() bool { return c.GitHubAppID != "" }

// GitHubLoginEnabled — пользователи могут связать GitHub-аккаунт: заданы Client ID и секрет App (§12.5).
func (c *Config) GitHubLoginEnabled() bool { return c.GitHubEnabled() && c.GitHubClientID != "" }

// checkGitHub: без GITHUB_APP_ID настройки не проверяются. С ним нужны ключ App и секрет
// вебхуков: без секрета подпись X-Hub-Signature-256 не проверить (§12.2).
func (c *Config) checkGitHub() error {
	if !c.GitHubEnabled() {
		return nil
	}
	if id, err := strconv.ParseInt(c.GitHubAppID, 10, 64); err != nil || id <= 0 {
		return errors.New("GITHUB_APP_ID: ожидается числовой id GitHub App")
	}
	if c.GitHubPrivateKeyFile == "" {
		return errors.New("GITHUB_APP_PRIVATE_KEY_FILE: при заданном GITHUB_APP_ID нужен путь к PEM-ключу App")
	}
	// Ключ читается при старте: неверный путь или не тот файл видны сразу, а не на первом вызове API.
	pem, err := os.ReadFile(c.GitHubPrivateKeyFile)
	if err != nil {
		return fmt.Errorf("GITHUB_APP_PRIVATE_KEY_FILE: %w", err)
	}
	if _, err := jwt.ParseRSAPrivateKeyFromPEM(pem); err != nil {
		return fmt.Errorf("GITHUB_APP_PRIVATE_KEY_FILE: не RSA-ключ в PEM: %w", err)
	}
	if len(c.GitHubWebhookSecret) < 16 || c.GitHubWebhookSecret == "change-me" {
		return errors.New("GITHUB_WEBHOOK_SECRET: при заданном GITHUB_APP_ID нужен секрет не короче 16 символов (не change-me)")
	}
	if (c.GitHubClientID == "") != (c.GitHubClientSecret == "") {
		return errors.New("GITHUB_CLIENT_ID и GITHUB_CLIENT_SECRET задаются вместе")
	}
	return nil
}

// TelegramEnabled — бот включён: задан токен.
func (c *Config) TelegramEnabled() bool { return c.TelegramBotToken != "" }

// TelegramWebhook — бот получает обновления через webhook (включён и не в long polling).
func (c *Config) TelegramWebhook() bool { return c.TelegramEnabled() && !c.TelegramPolling }

// webhookSecret — допустимый secret_token Telegram: 1–256 символов A-Z a-z 0-9 _ -.
var webhookSecret = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// checkTelegram: без токена настройки бота не проверяются (бот выключен). С токеном webhook
// без секрета не запускаем (§8.8: секрет проверяется на каждом запросе), а long polling — только
// в разработке.
func (c *Config) checkTelegram() error {
	if !c.TelegramEnabled() {
		return nil
	}
	// Без имени бота не разобрать команды вида /take@бот в группе и не собрать ссылку t.me (§8.1).
	if c.TelegramBotUsername == "" {
		return errors.New("TELEGRAM_BOT_USERNAME: при заданном TELEGRAM_BOT_TOKEN нужно имя бота")
	}
	if c.TelegramPolling {
		if c.Env == "production" {
			return errors.New("TELEGRAM_POLLING: long polling только для локальной разработки, в production — webhook")
		}
		return nil
	}
	if !webhookSecret.MatchString(c.TelegramWebhookSecret) || c.TelegramWebhookSecret == "change-me" {
		return errors.New("TELEGRAM_WEBHOOK_SECRET: при заданном TELEGRAM_BOT_TOKEN нужен секрет из 1–256 символов A-Z a-z 0-9 _ - (не change-me)")
	}
	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
