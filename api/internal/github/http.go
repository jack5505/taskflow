package github

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/project"
)

// Handlers — репозиторий GitHub в настройках проекта (§7.9, §12.1, §12.8).
type Handlers struct {
	svc         *Service
	projects    *project.Service
	requireAuth gin.HandlerFunc
}

func NewHandlers(svc *Service, projects *project.Service, requireAuth gin.HandlerFunc) *Handlers {
	return &Handlers{svc: svc, projects: projects, requireAuth: requireAuth}
}

func (h *Handlers) Register(api *gin.RouterGroup) {
	api.GET("/github/repositories", h.requireAuth, h.repositories)
	g := api.Group("/projects/:key/github", h.requireAuth)
	g.GET("", h.get)
	g.PUT("", h.bind)
	g.DELETE("", h.unbind)
}

// RepoBinding — репозиторий проекта; null — не привязан.
type RepoBinding struct {
	Repo *string `json:"repo" example:"acme/backend" extensions:"x-nullable"`
}

type bindRequest struct {
	Repo string `json:"repo" binding:"required,max=140" example:"acme/backend"`
}

// repositories — репозитории, доступные GitHub App.
//
// @Summary      Репозитории GitHub App
// @Description  Репозитории всех установок GitHub App — для выбора во вкладке «GitHub» настроек проекта (§12.1). Менеджер хотя бы одного проекта или администратор, иначе 403. Маршрут есть, только если задан GITHUB_APP_ID.
// @Tags         github
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   github.Repo
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Router       /api/v1/github/repositories [get]
func (h *Handlers) repositories(c *gin.Context) {
	repos, err := h.svc.Repositories(c.Request.Context(), auth.CurrentUser(c))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, repos)
}

// access — проект из пути: не участник — 404.
func (h *Handlers) access(c *gin.Context) (*project.Access, bool) {
	a, err := h.projects.Access(c.Request.Context(), auth.CurrentUser(c), c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return nil, false
	}
	return a, true
}

// get — репозиторий проекта.
//
// @Summary      Репозиторий GitHub проекта
// @Description  Привязанный репозиторий owner/name (§12.1); null — не привязан. Любой участник проекта.
// @Tags         github
// @Produce      json
// @Security     BearerAuth
// @Param        key  path      string  true  "ключ проекта"
// @Success      200  {object}  github.RepoBinding
// @Failure      401  {object}  apierr.Response
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/projects/{key}/github [get]
func (h *Handlers) get(c *gin.Context) {
	a, ok := h.access(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, RepoBinding{Repo: a.Project.GitHubRepo})
}

// bind — привязать репозиторий.
//
// @Summary      Привязать репозиторий GitHub
// @Description  Менеджер проекта привязывает репозиторий owner/name, в который установлен GitHub App (§12.1). Один репозиторий на проект и один проект на репозиторий (§12.7): занятый — 400. App не установлен — 400. Архивный проект — 409.
// @Tags         github
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        key   path      string                    true  "ключ проекта"
// @Param        body  body      github.bindRequest  true  "репозиторий"
// @Success      200   {object}  github.RepoBinding
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404   {object}  apierr.Response  "NOT_FOUND"
// @Failure      409   {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/github [put]
func (h *Handlers) bind(c *gin.Context) {
	a, ok := h.access(c)
	if !ok {
		return
	}
	if !a.CanManage() {
		httpserver.Abort(c, errManagerOnly)
		return
	}
	var req bindRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	p, err := h.svc.Bind(c.Request.Context(), a, req.Repo)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, RepoBinding{Repo: p.GitHubRepo})
}

// unbind — отвязать репозиторий.
//
// @Summary      Отвязать репозиторий GitHub
// @Description  Менеджер проекта отвязывает репозиторий (§12.1); не привязан — тоже 204. Архивный проект — 409.
// @Tags         github
// @Security     BearerAuth
// @Param        key  path  string  true  "ключ проекта"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Failure      409  {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/github [delete]
func (h *Handlers) unbind(c *gin.Context) {
	a, ok := h.access(c)
	if !ok {
		return
	}
	if !a.CanManage() {
		httpserver.Abort(c, errManagerOnly)
		return
	}
	if _, err := h.svc.Unbind(c.Request.Context(), a); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
