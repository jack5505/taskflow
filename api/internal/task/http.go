package task

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/project"
)

// Handlers — задачи (§9.2).
type Handlers struct {
	svc         *Service
	projects    *project.Service
	requireAuth gin.HandlerFunc
}

func NewHandlers(svc *Service, projects *project.Service, requireAuth gin.HandlerFunc) *Handlers {
	return &Handlers{svc: svc, projects: projects, requireAuth: requireAuth}
}

func (h *Handlers) Register(api *gin.RouterGroup) {
	api.POST("/projects/:key/tasks", h.requireAuth, h.create)
	t := api.Group("/tasks", h.requireAuth)
	t.GET("/:task_key", h.get)
	t.GET("/:task_key/history", h.history)
	t.POST("/:task_key/transition", h.transition)
	t.POST("/:task_key/take", h.take)
}

// createRequest — ограничения §3.3 (сервис проверяет их ещё раз: бот вызывает его напрямую).
type createRequest struct {
	Title           string     `json:"title" binding:"required,max=200"`
	Description     *string    `json:"description" binding:"omitempty,max=20000"`
	Type            string     `json:"type" binding:"omitempty,oneof=task bug feature" enums:"task,bug,feature"`
	Priority        string     `json:"priority" binding:"omitempty,oneof=low medium high critical" enums:"low,medium,high,critical"`
	AssigneeID      *uuid.UUID `json:"assignee_id"`
	SprintID        *uuid.UUID `json:"sprint_id"`
	EstimateMinutes *int       `json:"estimate_minutes" binding:"omitempty,min=1,max=60000"`
	DueDate         *string    `json:"due_date" binding:"omitempty,datetime=2006-01-02" example:"2026-10-31"`
	Labels          []string   `json:"labels" binding:"max=50,dive,max=200"` // ≤10 по ≤30 после обрезки и без повторов — проверяет сервис
}

// create — новая задача.
//
// @Summary      Создать задачу
// @Description  Любой участник проекта (§2). Номер — следующий в проекте, ключ вида TM-42, статус todo, автор — текущий пользователь. Исполнителя (активный участник) и спринт (не завершённый) при создании задаёт только менеджер или администратор. Поля и ограничения — §3.3. В архивном проекте — 409 PROJECT_ARCHIVED.
// @Tags         tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        key   path      string         true  "ключ проекта"
// @Param        body  body      createRequest  true  "задача"
// @Success      201   {object}  Task
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: исполнитель или спринт не от менеджера"
// @Failure      404   {object}  apierr.Response  "NOT_FOUND: нет проекта или пользователь не участник"
// @Failure      409   {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/tasks [post]
func (h *Handlers) create(c *gin.Context) {
	u := auth.CurrentUser(c)
	a, err := h.projects.Access(c.Request.Context(), u, c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	var req createRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	t, err := h.svc.Create(c.Request.Context(), a, u, CreateInput(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, t)
}

// get — карточка задачи.
//
// @Summary      Задача
// @Tags         tasks
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи, например TM-42"
// @Success      200       {object}  Task
// @Failure      401       {object}  apierr.Response
// @Failure      404       {object}  apierr.Response  "NOT_FOUND: нет задачи, она удалена или пользователь не участник проекта"
// @Router       /api/v1/tasks/{task_key} [get]
func (h *Handlers) get(c *gin.Context) {
	t, _, err := h.svc.Get(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

// history — история изменений задачи (§3.7).
//
// @Summary      История задачи
// @Tags         tasks
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи"
// @Success      200       {array}   HistoryView
// @Failure      401       {object}  apierr.Response
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/tasks/{task_key}/history [get]
func (h *Handlers) history(c *gin.Context) {
	t, _, err := h.svc.Get(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	list, err := h.svc.History(c.Request.Context(), t)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

type transitionRequest struct {
	To         string     `json:"to" binding:"required,oneof=todo in_progress review done cancelled" enums:"todo,in_progress,review,done,cancelled"`
	Comment    string     `json:"comment" binding:"max=5000"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
}

// transition — смена статуса по §4.
//
// @Summary      Сменить статус
// @Description  Переходы и права — §4: todo→in_progress (исполнитель; любой участник, если исполнителя нет — он им становится; менеджер), in_progress→todo (исполнитель, менеджер; исполнитель очищается), in_progress→review (исполнитель, менеджер), review→in_progress (автор, менеджер; comment от 5 символов), review→done (автор, если он не исполнитель; менеджер), done→in_progress (менеджер; если исполнитель деактивирован — assignee_id; из закрытого спринта — в активный или бэклог), todo/in_progress/review→cancelled (менеджер; comment обязателен), cancelled→todo (менеджер; исполнитель и спринт очищаются). Иной переход — 409 INVALID_TRANSITION.
// @Tags         tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string             true  "ключ задачи"
// @Param        body      body      transitionRequest  true  "новый статус"
// @Success      200       {object}  Task
// @Failure      400       {object}  apierr.Response  "VALIDATION_FAILED: нет причины, нужен активный исполнитель"
// @Failure      401       {object}  apierr.Response
// @Failure      403       {object}  apierr.Response  "FORBIDDEN: переход не по правам §4"
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "INVALID_TRANSITION, PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key}/transition [post]
func (h *Handlers) transition(c *gin.Context) {
	var req transitionRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	t, err := h.svc.Transition(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"), TransitionInput(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

// take — взять задачу себе.
//
// @Summary      Взять в работу
// @Description  Назначение себя и переход todo→in_progress одной транзакцией с блокировкой строки задачи (§8.4): из одновременных нажатий проходит одно. Задача уже у другого — 409 TASK_ALREADY_TAKEN (details.assignee); закрытая — 409 INVALID_TRANSITION. Повторный запрос того же исполнителя — 200 без изменений.
// @Tags         tasks
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи"
// @Success      200       {object}  Task
// @Failure      401       {object}  apierr.Response
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "TASK_ALREADY_TAKEN, INVALID_TRANSITION, PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key}/take [post]
func (h *Handlers) take(c *gin.Context) {
	t, err := h.svc.Take(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}
