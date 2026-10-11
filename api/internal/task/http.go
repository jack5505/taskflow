package task

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/apierr"
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
	t.PATCH("/:task_key", h.update)
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
	respond(c, http.StatusCreated, t)
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
	respond(c, http.StatusOK, t)
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
	respond(c, http.StatusOK, t)
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
	respond(c, http.StatusOK, t)
}

// respond — задача в ответе с ETag = version (клиент передаёт его в If-Match при PATCH).
func respond(c *gin.Context, status int, t *Task) {
	c.Header("ETag", strconv.Quote(strconv.Itoa(t.Version)))
	c.JSON(status, t)
}

func ifMatchError(reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Нужен заголовок If-Match с версией задачи").
		With("fields", []apierr.FieldError{{Field: "If-Match", Reason: reason}})
}

// ifMatch — версии из If-Match по RFC 9110 (сильное сравнение): "*" — без сверки (nil),
// иначе список сильных ETag вида "5", "6". Слабый W/"5" для If-Match не годится.
func ifMatch(c *gin.Context) ([]int, error) {
	h := strings.TrimSpace(c.GetHeader("If-Match"))
	if h == "" {
		return nil, ifMatchError("версия задачи из ETag карточки")
	}
	if h == "*" {
		return nil, nil
	}
	var versions []int
	for tag := range strings.SplitSeq(h, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if strings.HasPrefix(tag, "W/") {
			return nil, ifMatchError("нужен сильный ETag, без W/")
		}
		if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' {
			return nil, ifMatchError(`ETag в кавычках, например "5"`)
		}
		n, err := strconv.Atoi(tag[1 : len(tag)-1])
		if err != nil || n < 1 {
			return nil, ifMatchError("версия задачи из ETag карточки")
		}
		versions = append(versions, n)
	}
	if len(versions) == 0 {
		return nil, ifMatchError("версия задачи из ETag карточки")
	}
	return versions, nil
}

// updateRequest — поля PATCH; null очищает необязательное поле.
type updateRequest struct {
	Title           Opt[string]    `json:"title" swaggertype:"string"`
	Description     Opt[string]    `json:"description" swaggertype:"string" extensions:"x-nullable"`
	Type            Opt[string]    `json:"type" swaggertype:"string" enums:"task,bug,feature"`
	Priority        Opt[string]    `json:"priority" swaggertype:"string" enums:"low,medium,high,critical"`
	AssigneeID      Opt[uuid.UUID] `json:"assignee_id" swaggertype:"string" format:"uuid" extensions:"x-nullable"`
	SprintID        Opt[uuid.UUID] `json:"sprint_id" swaggertype:"string" format:"uuid" extensions:"x-nullable"`
	EstimateMinutes Opt[int]       `json:"estimate_minutes" swaggertype:"integer" extensions:"x-nullable"`
	DueDate         Opt[string]    `json:"due_date" swaggertype:"string" example:"2026-10-31" extensions:"x-nullable"`
	Labels          Opt[[]string]  `json:"labels" swaggertype:"array,string"`
	Status          Opt[string]    `json:"status" swaggerignore:"true"` // только чтобы отклонить: статус — через /transition
}

// update — изменение полей задачи.
//
// @Summary      Изменить задачу
// @Description  Меняются только переданные поля; null очищает необязательное поле; поле status — 400 (статус меняется через /transition). Заголовок If-Match — ETag карточки ("5"), список ETag или *; сравнение сильное, W/"5" — 400. Если задачу уже изменили — 409 VERSION_CONFLICT, в details.current актуальная задача. Права (§2): автор, исполнитель или менеджер; исполнитель (активный участник) и спринт (не завершённый) — только менеджер; задачу из завершённого спринта не переносят; оценку задачи, которая была в работе (в том числе потом отменённой), — только менеджер. Исполнителя нельзя менять в закрытой задаче и снимать с задачи в работе (§4).
// @Tags         tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string         true  "ключ задачи"
// @Param        If-Match  header    string         true  "ETag карточки (версия в кавычках), список ETag или *"
// @Param        body      body      updateRequest  true  "поля для изменения"
// @Success      200       {object}  Task
// @Failure      400       {object}  apierr.Response  "VALIDATION_FAILED, в том числе нет If-Match, слабый ETag, передан status"
// @Failure      401       {object}  apierr.Response
// @Failure      403       {object}  apierr.Response  "FORBIDDEN"
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "VERSION_CONFLICT, PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key} [patch]
func (h *Handlers) update(c *gin.Context) {
	versions, err := ifMatch(c)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	var req updateRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	if req.Status.Set {
		httpserver.Abort(c, fieldError("status", "статус меняется через POST /tasks/{key}/transition"))
		return
	}
	in := UpdateInput{
		Title: req.Title, Description: req.Description, Type: req.Type, Priority: req.Priority,
		AssigneeID: req.AssigneeID, SprintID: req.SprintID, EstimateMinutes: req.EstimateMinutes,
		DueDate: req.DueDate, Labels: req.Labels,
	}
	t, err := h.svc.Update(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"), versions, in)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	respond(c, http.StatusOK, t)
}
