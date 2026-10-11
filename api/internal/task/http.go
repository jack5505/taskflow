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
	api.GET("/projects/:key/tasks", h.requireAuth, h.list)
	api.GET("/me/tasks", h.requireAuth, h.mine)
	t := api.Group("/tasks", h.requireAuth)
	t.GET("/:task_key", h.get)
	t.PATCH("/:task_key", h.update)
	t.DELETE("/:task_key", h.delete)
	t.POST("/:task_key/restore", h.restore)
	t.POST("/:task_key/move", h.move)
	t.GET("/:task_key/comments", h.comments)
	t.POST("/:task_key/comments", h.addComment)
	c := api.Group("/comments", h.requireAuth)
	c.PATCH("/:id", h.updateComment)
	c.DELETE("/:id", h.deleteComment)
	t.GET("/:task_key/history", h.history)
	t.POST("/:task_key/transition", h.transition)
	t.POST("/:task_key/take", h.take)
	h.registerAttachments(api)
	h.registerWorklogs(api)
	h.registerTimer(api)
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

// multi — значения параметра, повторённого и/или через запятую.
func multi(c *gin.Context, name string) []string {
	var out []string
	for _, v := range c.QueryArray(name) {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func idFilter(c *gin.Context, name string) (IDFilter, error) {
	var f IDFilter
	for _, v := range multi(c, name) {
		if v == "null" {
			f.Null = true
			continue
		}
		id, err := uuid.Parse(v)
		if err != nil {
			return f, fieldError(name, "UUID или null")
		}
		f.IDs = append(f.IDs, id)
	}
	return f, nil
}

// list — задачи проекта.
//
// @Summary      Задачи проекта
// @Description  Фильтры (повторяемые или через запятую): status, type, priority; assignee_id и sprint_id — UUID или null (без исполнителя / бэклог); label; q — поиск по ключу (с начала) и названию. include_deleted — только администратор. Порядок — ручной порядок секции (position), затем номер; limit до 200 (по умолчанию 50), следующая страница — cursor из next_cursor. overdue — срок прошёл по поясу проекта, а задача не закрыта.
// @Tags         tasks
// @Produce      json
// @Security     BearerAuth
// @Param        key              path      string    true   "ключ проекта"
// @Param        status           query     []string  false  "статусы"  collectionFormat(multi)
// @Param        type             query     []string  false  "типы"  collectionFormat(multi)
// @Param        priority         query     []string  false  "приоритеты"  collectionFormat(multi)
// @Param        assignee_id      query     []string  false  "исполнители; null — без исполнителя"  collectionFormat(multi)
// @Param        sprint_id        query     []string  false  "спринты; null — бэклог"  collectionFormat(multi)
// @Param        label            query     string    false  "метка"
// @Param        q                query     string    false  "поиск по ключу и названию"
// @Param        include_deleted  query     bool      false  "с удалёнными (администратор)"
// @Param        limit            query     int       false  "до 200"
// @Param        cursor           query     string    false  "курсор следующей страницы"
// @Success      200              {object}  Page
// @Failure      400              {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401              {object}  apierr.Response
// @Failure      403              {object}  apierr.Response  "FORBIDDEN: include_deleted не администратору"
// @Failure      404              {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/projects/{key}/tasks [get]
func (h *Handlers) list(c *gin.Context) {
	a, err := h.projects.Access(c.Request.Context(), auth.CurrentUser(c), c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	f := ListFilter{Status: multi(c, "status"), Type: multi(c, "type"), Priority: multi(c, "priority"),
		Label: c.Query("label"), Q: c.Query("q"), Cursor: c.Query("cursor")}
	if f.Assignee, err = idFilter(c, "assignee_id"); err == nil {
		f.Sprint, err = idFilter(c, "sprint_id")
	}
	if err == nil && c.Query("include_deleted") != "" {
		if f.IncludeDeleted, err = strconv.ParseBool(c.Query("include_deleted")); err != nil {
			err = fieldError("include_deleted", "true или false")
		}
	}
	if err == nil && c.Query("limit") != "" {
		if f.Limit, err = strconv.Atoi(c.Query("limit")); err != nil {
			err = fieldError("limit", "целое число")
		}
	}
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	page, err := h.svc.List(c.Request.Context(), a, f)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// mine — мои открытые задачи по всем проектам.
//
// @Summary      Мои задачи
// @Description  Открытые задачи текущего пользователя по всем его проектам, кроме архивных (§7.2), по сроку: без срока — в конце, просроченные — сверху. Не больше 200 самых срочных.
// @Tags         tasks
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   MyTask
// @Failure      401  {object}  apierr.Response
// @Router       /api/v1/me/tasks [get]
func (h *Handlers) mine(c *gin.Context) {
	list, err := h.svc.Mine(c.Request.Context(), auth.CurrentUser(c))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// delete — мягкое удаление задачи.
//
// @Summary      Удалить задачу
// @Description  Мягкое удаление (§4): задача пропадает с доски, из бэклога, поиска и бота; записи времени остаются в отчётах. Менеджер проекта или администратор. Восстановление — POST /tasks/{key}/restore (администратор).
// @Tags         tasks
// @Security     BearerAuth
// @Param        task_key  path  string  true  "ключ задачи"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Failure      409  {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key} [delete]
func (h *Handlers) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key")); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// restore — восстановление удалённой задачи.
//
// @Summary      Восстановить задачу
// @Description  Только администратор системы (§2). Не удалённая задача возвращается без изменений.
// @Tags         tasks
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи"
// @Success      200       {object}  Task
// @Failure      401       {object}  apierr.Response
// @Failure      403       {object}  apierr.Response  "FORBIDDEN: не администратор"
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key}/restore [post]
func (h *Handlers) restore(c *gin.Context) {
	t, err := h.svc.Restore(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	respond(c, http.StatusOK, t)
}

type moveRequest struct {
	SprintID    Opt[uuid.UUID] `json:"sprint_id" swaggertype:"string" format:"uuid" extensions:"x-nullable"`
	AfterTaskID *uuid.UUID     `json:"after_task_id" swaggertype:"string" format:"uuid" extensions:"x-nullable"`
}

// move — перетаскивание задачи.
//
// @Summary      Переместить задачу
// @Description  §7.4: sprint_id — секция (null — бэклог; не передан — та же секция), after_task_id — после какой задачи этой секции (null — в начало). Смена секции — только менеджер; порядок внутри секции — любой участник. В завершённый спринт и из него переносить нельзя. Перенос в активный спринт и из него пишется в журнал спринта.
// @Tags         tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string       true  "ключ задачи"
// @Param        body      body      moveRequest  true  "куда"
// @Success      200       {object}  Task
// @Failure      400       {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401       {object}  apierr.Response
// @Failure      403       {object}  apierr.Response  "FORBIDDEN: смена спринта не менеджером"
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key}/move [post]
func (h *Handlers) move(c *gin.Context) {
	var req moveRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	t, err := h.svc.Move(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"), MoveInput(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	respond(c, http.StatusOK, t)
}

type commentRequest struct {
	Body string `json:"body" binding:"required,max=20000"` // 1–5000 после обрезки пробелов — сервис
}

// comments — комментарии задачи.
//
// @Summary      Комментарии
// @Tags         comments
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи"
// @Success      200       {array}   CommentView
// @Failure      401       {object}  apierr.Response
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/tasks/{task_key}/comments [get]
func (h *Handlers) comments(c *gin.Context) {
	list, err := h.svc.Comments(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// addComment — новый комментарий.
//
// @Summary      Добавить комментарий
// @Description  Любой участник проекта (§2). Markdown, 1–5000 символов (§3.6). Событие comment.created.
// @Tags         comments
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string          true  "ключ задачи"
// @Param        body      body      commentRequest  true  "текст"
// @Success      201       {object}  CommentView
// @Failure      400       {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401       {object}  apierr.Response
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key}/comments [post]
func (h *Handlers) addComment(c *gin.Context) {
	var req commentRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	v, err := h.svc.AddComment(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"), req.Body)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, v)
}

func commentID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpserver.Abort(c, ErrCommentNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// updateComment — правка комментария.
//
// @Summary      Изменить комментарий
// @Description  Автор комментария или менеджер проекта (§3.6).
// @Tags         comments
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string          true  "id комментария"
// @Param        body  body      commentRequest  true  "новый текст"
// @Success      200   {object}  CommentView
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: чужой комментарий"
// @Failure      404   {object}  apierr.Response  "NOT_FOUND"
// @Failure      409   {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/comments/{id} [patch]
func (h *Handlers) updateComment(c *gin.Context) {
	id, ok := commentID(c)
	if !ok {
		return
	}
	var req commentRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	v, err := h.svc.UpdateComment(c.Request.Context(), auth.CurrentUser(c), id, req.Body)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// deleteComment — удаление комментария.
//
// @Summary      Удалить комментарий
// @Description  Автор комментария или менеджер проекта (§2: чужие — только менеджер).
// @Tags         comments
// @Security     BearerAuth
// @Param        id  path  string  true  "id комментария"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: чужой комментарий"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Failure      409  {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/comments/{id} [delete]
func (h *Handlers) deleteComment(c *gin.Context) {
	id, ok := commentID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteComment(c.Request.Context(), auth.CurrentUser(c), id); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
