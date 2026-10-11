package telegram

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/project"
)

// GroupHandlers — Telegram-группа в настройках проекта (§7.9, §9.2):
// POST /projects/{key}/telegram-bind-code и DELETE /projects/{key}/telegram.
type GroupHandlers struct {
	groups      *Groups
	projects    *project.Service
	requireAuth gin.HandlerFunc
	botEnabled  bool
}

// NewGroupHandlers — botEnabled=false (нет TELEGRAM_BOT_TOKEN): код выдавать незачем, маршрута
// нет (404); отключить группу можно всегда.
func NewGroupHandlers(groups *Groups, projects *project.Service, requireAuth gin.HandlerFunc, botEnabled bool) *GroupHandlers {
	return &GroupHandlers{groups: groups, projects: projects, requireAuth: requireAuth, botEnabled: botEnabled}
}

func (h *GroupHandlers) Register(api *gin.RouterGroup) {
	g := api.Group("/projects/:key", h.requireAuth)
	if h.botEnabled {
		g.POST("/telegram-bind-code", h.bindCode)
	}
	g.DELETE("/telegram", h.disconnect)
}

// manage — проект из пути и права менеджера (§2): не участник — 404, участник — 403.
// Сервис перепроверяет роль под блокировкой проекта.
func (h *GroupHandlers) manage(c *gin.Context) (*project.Access, bool) {
	a, err := h.projects.Access(c.Request.Context(), auth.CurrentUser(c), c.Param("key"))
	if err == nil && !a.CanManage() {
		err = project.ErrForbidden
	}
	if err != nil {
		httpserver.Abort(c, err)
		return nil, false
	}
	return a, true
}

// bindCode — код подключения группы.
//
// @Summary      Код подключения Telegram-группы
// @Description  Одноразовый код из 8 символов на 15 минут (§8.2): менеджер добавляет бота в группу и пишет в ней /bind <код>. Новый код гасит прежний неиспользованный код проекта. Менеджер проекта или администратор; в архивном проекте — 409. Маршрут есть, только если бот подключён (telegram_bot_enabled в GET /me), иначе 404.
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        key  path      string  true  "ключ проекта"
// @Success      201  {object}  telegram.BindCode
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND: нет проекта, пользователь не участник или бот не подключён"
// @Failure      409  {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/telegram-bind-code [post]
func (h *GroupHandlers) bindCode(c *gin.Context) {
	a, ok := h.manage(c)
	if !ok {
		return
	}
	code, err := h.groups.IssueBindCode(c.Request.Context(), a)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, code)
}

// disconnect — отключить группу.
//
// @Summary      Отключить Telegram-группу
// @Description  Кнопка «Отключить» в настройках проекта (§8.2, шаг 5): группа отвязывается от проекта, другие проекты этой группы не меняются. Группа не подключена — тоже 204. Менеджер проекта или администратор; в архивном проекте — 409.
// @Tags         projects
// @Security     BearerAuth
// @Param        key  path  string  true  "ключ проекта"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Failure      409  {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/telegram [delete]
func (h *GroupHandlers) disconnect(c *gin.Context) {
	a, ok := h.manage(c)
	if !ok {
		return
	}
	if err := h.groups.Disconnect(c.Request.Context(), a); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
