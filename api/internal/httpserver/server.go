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
	"github.com/redis/go-redis/v9"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/config"
	_ "github.com/jack5505/taskflow/api/internal/docs" // сгенерированная спецификация Swagger (make swag)
	"github.com/jack5505/taskflow/api/internal/web"
)

// Server держит зависимости обработчиков. Домены добавляют сюда свои сервисы.
type Server struct {
	cfg   *config.Config
	db    *gorm.DB
	redis *redis.Client
}

// New собирает сервер с зависимостями.
func New(cfg *config.Config, g *gorm.DB, rdb *redis.Client) *Server {
	return &Server{cfg: cfg, db: g, redis: rdb}
}

// Run поднимает HTTP-сервер и ждёт отмены контекста.
func (s *Server) Run(ctx context.Context) error {
	if s.cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api: listening", "addr", s.cfg.Addr)
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

// Router собирает маршруты. Базовый путь API — /api/v1 (ТЗ §9).
func (s *Server) Router() *gin.Engine {
	r := gin.New()
	// accessLog снаружи recovery: строка лога пишется и для запроса, который упал с паникой.
	r.Use(requestID(), accessLog(), recovery())
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) {
		Abort(c, apierr.New(http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed, "Метод не поддерживается"))
	})

	r.GET("/health", s.healthHandler)

	// Swagger UI и спецификация: /api/docs/index.html, /api/docs/doc.json (ТЗ §10.3).
	r.GET("/api/docs", docsRedirect)
	r.Any("/api/docs/*any", docsHandler(ginSwagger.WrapHandler(swaggerFiles.Handler)))

	api := r.Group("/api/v1")
	api.GET("/ping", s.pingHandler)

	mountSPA(r)
	return r
}

// docsRedirect ведёт на UI. 302, а не 301: браузер не должен кэшировать внутренний редирект навсегда.
func docsRedirect(c *gin.Context) {
	c.Redirect(http.StatusFound, "/api/docs/index.html")
}

// docsFiles — то, что отдаёт gin-swagger; остальное под /api/docs отвечает 404 и 405 в формате §9.3,
// а не plain-text ответами самого gin-swagger.
var docsFiles = map[string]bool{
	"index.html": true, "doc.json": true, "doc.yaml": true, "swagger-initializer.js": true,
	"oauth2-redirect.html": true, "favicon-16x16.png": true, "favicon-32x32.png": true,
	"swagger-ui.css": true, "swagger-ui.css.map": true, "swagger-ui-bundle.js": true, "swagger-ui-bundle.js.map": true,
	"swagger-ui-standalone-preset.js": true, "swagger-ui-standalone-preset.js.map": true,
}

func docsHandler(ui gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimPrefix(c.Param("any"), "/")
		switch {
		case name == "":
			docsRedirect(c)
		case !docsFiles[name]:
			Abort(c, apierr.NotFound("Маршрут не найден"))
		case c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead:
			Abort(c, apierr.New(http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed, "Метод не поддерживается"))
		default:
			ui(c)
		}
	}
}

// pingHandler — проверка, что API отвечает; без авторизации.
//
// @Summary      Проверка связи
// @Tags         system
// @Produce      json
// @Success      200  {object}  map[string]bool  "pong: true"
// @Router       /api/v1/ping [get]
func (s *Server) pingHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"pong": true})
}

// mountSPA отдаёт собранный веб-интерфейс, встроенный в бинарник (ТЗ §10.2).
// Любой путь вне /api и /health возвращает index.html — маршрутизация на стороне React;
// неизвестный путь под /api отвечает 404 в формате §9.3.
func mountSPA(r *gin.Engine) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(dist))

	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") {
			Abort(c, apierr.NotFound("Маршрут не найден"))
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
