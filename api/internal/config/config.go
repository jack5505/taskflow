// Package config читает настройки из переменных окружения (ТЗ §10.3: секреты только в env).
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
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

	TelegramBotToken      string
	TelegramWebhookSecret string
	TelegramBotUsername   string

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
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
