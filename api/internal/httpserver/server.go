// Package httpserver — HTTP-слой на Gin (ТЗ §9, §10.2).
package httpserver

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/web"
)

// Run поднимает сервер и ждёт отмены контекста.
func Run(ctx context.Context, cfg *config.Config) error {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := NewRouter(cfg)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api: listening", "addr", cfg.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// NewRouter собирает маршруты. Базовый путь API — /api/v1 (ТЗ §9).
func NewRouter(cfg *config.Config) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), requestID(), accessLog())

	// /health проверяет базу и Redis (ТЗ §10.3) — проверки появятся вместе с подключением к ним.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pong": true})
	})

	mountSPA(r)
	return r
}

// mountSPA отдаёт собранный веб-интерфейс, встроенный в бинарник (ТЗ §10.2).
// Любой путь вне /api и /health возвращает index.html — маршрутизация на стороне React.
func mountSPA(r *gin.Engine) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(dist))

	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "NOT_FOUND", "message": "route not found"}})
			return
		}
		if _, err := fs.Stat(dist, strings.TrimPrefix(p, "/")); err == nil && p != "/" {
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}
