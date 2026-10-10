package project

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// Handlers — /api/v1/projects (ТЗ §9.2).
type Handlers struct {
	svc         *Service
	requireAuth gin.HandlerFunc
}

func NewHandlers(svc *Service, requireAuth gin.HandlerFunc) *Handlers {
	return &Handlers{svc: svc, requireAuth: requireAuth}
}

// Register добавляет маршруты в группу /api/v1.
func (h *Handlers) Register(api *gin.RouterGroup) {
	g := api.Group("/projects", h.requireAuth)
	g.GET("", h.list)
	g.POST("", auth.RequireSystemAdmin(), h.create)
	g.GET("/:key", h.get)
	g.PATCH("/:key", h.update)
	g.DELETE("/:key", auth.RequireSystemAdmin(), h.delete)
	g.POST("/:key/restore", auth.RequireSystemAdmin(), h.restore)
}

func boolQuery(c *gin.Context, name string) (bool, error) {
	v := c.Query(name)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
			With("fields", []apierr.FieldError{{Field: name, Reason: "ожидается true или false"}})
	}
	return b, nil
}

// list — проекты пользователя.
//
// @Summary      Проекты
// @Description  Проекты, где пользователь участник; администратору системы — все. По умолчанию — не архивные; archived=true — только архив; deleted=true — только удалённые (администратор).
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        archived  query     bool  false  "только архивные"
// @Param        deleted   query     bool  false  "только удалённые (администратор)"
// @Success      200       {array}   View
// @Failure      400       {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401       {object}  apierr.Response
// @Failure      403       {object}  apierr.Response  "FORBIDDEN: deleted=true не администратору"
// @Router       /api/v1/projects [get]
func (h *Handlers) list(c *gin.Context) {
	var f ListFilter
	var err error
	if f.Archived, err = boolQuery(c, "archived"); err == nil {
		f.Deleted, err = boolQuery(c, "deleted")
	}
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	list, err := h.svc.List(c.Request.Context(), auth.CurrentUser(c), f)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

type createRequest struct {
	Key         string  `json:"key" binding:"required,max=10" example:"TM"`
	Name        string  `json:"name" binding:"required,max=300"`
	Description *string `json:"description" binding:"omitempty,max=6000"`
	Timezone    string  `json:"timezone" binding:"max=64" example:"Europe/Moscow"`
}

// create — новый проект.
//
// @Summary      Создать проект
// @Description  Ключ — 2–10 заглавных латинских букв, уникален и среди удалённых проектов, не меняется. Участников добавляет администратор или менеджер (§9.2, участники).
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      createRequest  true  "проект"
// @Success      201   {object}  View
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED, в том числе ключ занят"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: не администратор системы"
// @Router       /api/v1/projects [post]
func (h *Handlers) create(c *gin.Context) {
	var req createRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	p, err := h.svc.Create(c.Request.Context(), CreateInput(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, newView(p, ""))
}

// get — карточка проекта.
//
// @Summary      Проект
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        key  path      string  true  "ключ проекта"
// @Success      200  {object}  View
// @Failure      401  {object}  apierr.Response
// @Failure      404  {object}  apierr.Response  "NOT_FOUND: нет, удалён или пользователь не участник"
// @Router       /api/v1/projects/{key} [get]
func (h *Handlers) get(c *gin.Context) {
	a, err := h.svc.Access(c.Request.Context(), auth.CurrentUser(c), c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, a.View())
}

type updateRequest struct {
	Name                 *string         `json:"name" binding:"omitempty,max=300"`
	Description          *string         `json:"description" binding:"omitempty,max=6000"`
	Timezone             *string         `json:"timezone" binding:"omitempty,max=64"`
	NotificationSettings map[string]bool `json:"notification_settings"`
	DailySummaryTime     *string         `json:"daily_summary_time" binding:"omitempty,max=5" example:"10:00"`
	IsArchived           *bool           `json:"is_archived"`
}

// update — изменение проекта.
//
// @Summary      Изменить проект
// @Description  Менеджер проекта или администратор. Меняются только переданные поля; description "" очищает. notification_settings — частичный объект событий §8.6 (task_created, task_taken, task_returned, task_review, task_rework, task_accepted, task_cancelled, sprint_started, sprint_closed, daily_summary, comment_created). is_archived — только администратор. В архивном проекте любое изменение, кроме разархивации, — 409 PROJECT_ARCHIVED.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        key   path      string         true  "ключ проекта"
// @Param        body  body      updateRequest  true  "поля для изменения"
// @Success      200   {object}  View
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: не менеджер; is_archived — не администратор"
// @Failure      404   {object}  apierr.Response  "NOT_FOUND"
// @Failure      409   {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key} [patch]
func (h *Handlers) update(c *gin.Context) {
	a, err := h.svc.Access(c.Request.Context(), auth.CurrentUser(c), c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	var req updateRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	p, err := h.svc.Update(c.Request.Context(), a, UpdateInput(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, newView(p, a.Role))
}

// delete — мягкое удаление проекта.
//
// @Summary      Удалить проект
// @Description  Мягкое удаление (§3.2): проект пропадает из списков, бота и отчётов, Telegram-группа отключается; данные сохраняются. Восстановление — POST /projects/{key}/restore.
// @Tags         projects
// @Security     BearerAuth
// @Param        key  path  string  true  "ключ проекта"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не администратор системы"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/projects/{key} [delete]
func (h *Handlers) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("key")); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// restore — восстановление удалённого проекта.
//
// @Summary      Восстановить проект
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        key  path      string  true  "ключ проекта"
// @Success      200  {object}  View
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не администратор системы"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/projects/{key}/restore [post]
func (h *Handlers) restore(c *gin.Context) {
	p, err := h.svc.Restore(c.Request.Context(), c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, newView(p, ""))
}
