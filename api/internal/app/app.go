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
	"github.com/jack5505/taskflow/api/internal/users"
)

// NewServer — API со всеми маршрутами. jobs — очередь заданий воркера (письма).
func NewServer(cfg *config.Config, g *gorm.DB, rdb *redis.Client, jobs *asynq.Client) *httpserver.Server {
	authSvc := auth.NewService(cfg, g, rdb).WithQueue(jobs)
	authH := auth.NewHandlers(authSvc, auth.SecureCookies(cfg.BaseURL))
	requireAuth := authH.RequireAuth()
	return httpserver.New(cfg, g, rdb).
		Mount(authH.Register).
		Mount(users.NewHandlers(users.NewService(g, authSvc), requireAuth).Register).
		Mount(project.NewHandlers(project.NewService(g), requireAuth).Register).
		Mount(me.NewHandlers(me.NewService(g), requireAuth).Register)
}
