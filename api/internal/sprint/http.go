package sprint

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// Handlers — спринты (§9.2).
type Handlers struct {
	svc         *Service
	projects    *project.Service
	requireAuth gin.HandlerFunc
}

func NewHandlers(svc *Service, projects *project.Service, requireAuth gin.HandlerFunc) *Handlers {
	return &Handlers{svc: svc, projects: projects, requireAuth: requireAuth}
}

func (h *Handlers) Register(api *gin.RouterGroup) {
	api.GET("/projects/:key/sprints", h.requireAuth, h.list)
	api.POST("/projects/:key/sprints", h.requireAuth, h.create)
	g := api.Group("/sprints", h.requireAuth)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update)
	g.DELETE("/:id", h.delete)
	g.POST("/:id/start", h.start)
}

// request — поля спринта; null у goal и дат очищает их.
type request struct {
	Name      *string          `json:"name" binding:"omitempty,max=300"`
	Goal      task.Opt[string] `json:"goal" swaggertype:"string" extensions:"x-nullable"`
	StartDate task.Opt[string] `json:"start_date" swaggertype:"string" example:"2026-10-13" extensions:"x-nullable"`
	EndDate   task.Opt[string] `json:"end_date" swaggertype:"string" example:"2026-10-24" extensions:"x-nullable"`
}

func (r request) input() Input {
	in := Input{Name: r.Name, Goal: r.Goal.Value, StartDate: r.StartDate.Value, EndDate: r.EndDate.Value}
	in.ClearGoal = r.Goal.Set && r.Goal.Value == nil
	in.ClearDate.Start = r.StartDate.Set && r.StartDate.Value == nil
	in.ClearDate.End = r.EndDate.Set && r.EndDate.Value == nil
	return in
}

func id(c *gin.Context) (uuid.UUID, bool) {
	v, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpserver.Abort(c, ErrNotFound)
		return uuid.Nil, false
	}
	return v, true
}

// list — спринты проекта.
//
// @Summary      Спринты проекта
// @Description  Участник проекта. Порядок: активный, запланированные по дате начала, завершённые (новые первыми). tasks_count и estimate_minutes — итог секции (§7.4), overdue — активный спринт после end_date (§5.3).
// @Tags         sprints
// @Produce      json
// @Security     BearerAuth
// @Param        key  path      string  true  "ключ проекта"
// @Success      200  {array}   View
// @Failure      401  {object}  apierr.Response
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/projects/{key}/sprints [get]
func (h *Handlers) list(c *gin.Context) {
	a, err := h.projects.Access(c.Request.Context(), auth.CurrentUser(c), c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	list, err := h.svc.List(c.Request.Context(), a)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// create — новый спринт.
//
// @Summary      Создать спринт
// @Description  Менеджер (§2, §5.1). Состояние planned; без названия — «Спринт N»; даты необязательны до запуска, end_date позже start_date и не дальше 42 дней.
// @Tags         sprints
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        key   path      string   true  "ключ проекта"
// @Param        body  body      request  true  "спринт"
// @Success      201   {object}  View
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404   {object}  apierr.Response  "NOT_FOUND"
// @Failure      409   {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/sprints [post]
func (h *Handlers) create(c *gin.Context) {
	a, err := h.projects.Access(c.Request.Context(), auth.CurrentUser(c), c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	var req request
	if !httpserver.Bind(c, &req) {
		return
	}
	v, err := h.svc.Create(c.Request.Context(), a, req.input())
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, v)
}

// get — карточка спринта.
//
// @Summary      Спринт
// @Tags         sprints
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "id спринта"
// @Success      200  {object}  View
// @Failure      401  {object}  apierr.Response
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/sprints/{id} [get]
func (h *Handlers) get(c *gin.Context) {
	sid, ok := id(c)
	if !ok {
		return
	}
	v, _, err := h.svc.Get(c.Request.Context(), auth.CurrentUser(c), sid)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// update — изменение спринта.
//
// @Summary      Изменить спринт
// @Description  Менеджер. Меняются только переданные поля; null очищает цель и даты (у активного даты обязательны). Завершённый спринт не изменяется — 409 INVALID_TRANSITION (details.reason = closed).
// @Tags         sprints
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string   true  "id спринта"
// @Param        body  body      request  true  "поля"
// @Success      200   {object}  View
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN"
// @Failure      404   {object}  apierr.Response  "NOT_FOUND"
// @Failure      409   {object}  apierr.Response  "INVALID_TRANSITION, PROJECT_ARCHIVED"
// @Router       /api/v1/sprints/{id} [patch]
func (h *Handlers) update(c *gin.Context) {
	sid, ok := id(c)
	if !ok {
		return
	}
	var req request
	if !httpserver.Bind(c, &req) {
		return
	}
	v, err := h.svc.Update(c.Request.Context(), auth.CurrentUser(c), sid, req.input())
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// delete — удаление запланированного спринта.
//
// @Summary      Удалить спринт
// @Description  Менеджер; только planned (§5.5), его задачи возвращаются в бэклог. Иначе — 409 INVALID_TRANSITION.
// @Tags         sprints
// @Security     BearerAuth
// @Param        id  path  string  true  "id спринта"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Failure      409  {object}  apierr.Response  "INVALID_TRANSITION, PROJECT_ARCHIVED"
// @Router       /api/v1/sprints/{id} [delete]
func (h *Handlers) delete(c *gin.Context) {
	sid, ok := id(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), auth.CurrentUser(c), sid); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type startResponse struct {
	Sprint View  `json:"sprint"`
	Scope  Scope `json:"scope"`
}

// start — запуск спринта.
//
// @Summary      Запустить спринт
// @Description  Менеджер (§5.2). Под блокировкой проекта: в проекте нет активного (иначе 409 SPRINT_ALREADY_ACTIVE), даты заполнены, end позже start, не больше 42 дней, в спринте есть задачи (иначе 409 INVALID_TRANSITION, details.reason: dates | empty | not_planned). Фиксирует объём на старте: задачи, сумма оценок, сколько без оценки.
// @Tags         sprints
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "id спринта"
// @Success      200  {object}  startResponse
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Failure      409  {object}  apierr.Response  "SPRINT_ALREADY_ACTIVE, INVALID_TRANSITION, PROJECT_ARCHIVED"
// @Router       /api/v1/sprints/{id}/start [post]
func (h *Handlers) start(c *gin.Context) {
	sid, ok := id(c)
	if !ok {
		return
	}
	v, scope, err := h.svc.Start(c.Request.Context(), auth.CurrentUser(c), sid)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, startResponse{Sprint: *v, Scope: *scope})
}
