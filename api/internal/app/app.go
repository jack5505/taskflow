// Package app собирает HTTP-сервер из всех доменов: одно место и для `taskflow api`,
// и для тестов прав по таблице §2 на полном наборе маршрутов.
package app

import (
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/me"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/report"
	"github.com/jack5505/taskflow/api/internal/sprint"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/users"
)

// NewServer — API со всеми маршрутами. jobs — очередь заданий воркера (письма, обновления бота).
// Webhook Telegram есть, только если бот включён (TELEGRAM_BOT_TOKEN) и работает не в long polling.
func NewServer(cfg *config.Config, g *gorm.DB, rdb *redis.Client, jobs *asynq.Client) *httpserver.Server {
	authSvc := auth.NewService(cfg, g, rdb).WithQueue(jobs)
	authH := auth.NewHandlers(authSvc, auth.SecureCookies(cfg.BaseURL))
	requireAuth := authH.RequireAuth()
	projects := project.NewService(g)
	tasks := task.NewService(g, projects).WithAttachments(cfg.AttachmentsDir, cfg.AttachmentMaxBytes, cfg.JWTSecret)
	srv := httpserver.New(cfg, g, rdb)
	if cfg.TelegramEnabled() && !cfg.TelegramPolling {
		srv.Mount(telegram.NewWebhook(cfg.TelegramWebhookSecret, telegram.NewIntake(rdb, jobs)).Register)
	}
	return srv.
		Mount(authH.Register).
		Mount(users.NewHandlers(users.NewService(g, authSvc), requireAuth).Register).
		Mount(project.NewHandlers(projects, requireAuth).Register).
		Mount(task.NewHandlers(tasks, projects, requireAuth).Register).
		Mount(sprint.NewHandlers(sprint.NewService(g, projects), projects, requireAuth).Register).
		Mount(report.NewHandlers(report.NewService(g, projects), requireAuth).Register).
		Mount(me.NewHandlers(me.NewService(g), requireAuth).Register)
}
