package project

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
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
	g.GET("/:key/members", h.members)
	g.PUT("/:key/members/:user_id", h.putMember)
	g.DELETE("/:key/members/:user_id", h.removeMember)
	g.GET("/:key/audit", auth.RequireSystemAdmin(), h.auditLog)
}

// access — проект из пути и роль текущего пользователя; не участник — 404 (§2).
func (h *Handlers) access(c *gin.Context) (*Access, bool) {
	a, err := h.svc.Access(c.Request.Context(), auth.CurrentUser(c), c.Param("key"))
	if err != nil {
		httpserver.Abort(c, err)
		return nil, false
	}
	return a, true
}

// manage — access + права менеджера (§2). Сервис перепроверяет роль под блокировкой.
func (h *Handlers) manage(c *gin.Context) (*Access, bool) {
	a, ok := h.access(c)
	if ok && !a.CanManage() {
		httpserver.Abort(c, ErrForbidden)
		return nil, false
	}
	return a, ok
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
	Name        string  `json:"name" binding:"required,max=100"`
	Description *string `json:"description" binding:"omitempty,max=2000"`
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
	a, ok := h.access(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, a.View())
}

type updateRequest struct {
	Name                 *string         `json:"name" binding:"omitempty,max=100"`
	Description          *string         `json:"description" binding:"omitempty,max=2000"`
	Timezone             *string         `json:"timezone" binding:"omitempty,max=64"`
	NotificationSettings map[string]bool `json:"notification_settings"`
	DailySummaryTime     *string         `json:"daily_summary_time" binding:"omitempty,max=5" example:"10:00"`
	IsArchived           *bool           `json:"is_archived"`
}

// update — изменение проекта.
//
// @Summary      Изменить проект
// @Description  Менеджер проекта или администратор. Меняются только переданные поля; description "" очищает. notification_settings — частичный объект событий §8.6 (task_created, task_taken, task_returned, task_review, task_rework, task_accepted, task_cancelled, sprint_started, sprint_closed, daily_summary, comment_created). смену is_archived — только администратор (то же значение, что сейчас, пропускается). В архивном проекте любое изменение, кроме разархивации, — 409 PROJECT_ARCHIVED.
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
	a, ok := h.manage(c)
	if !ok {
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

// members — участники проекта.
//
// @Summary      Участники проекта
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        key  path      string  true  "ключ проекта"
// @Success      200  {array}   MemberView
// @Failure      401  {object}  apierr.Response
// @Failure      404  {object}  apierr.Response  "NOT_FOUND: нет проекта или пользователь не участник"
// @Router       /api/v1/projects/{key}/members [get]
func (h *Handlers) members(c *gin.Context) {
	a, ok := h.access(c)
	if !ok {
		return
	}
	list, err := h.svc.Members(c.Request.Context(), a)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

type putMemberRequest struct {
	Role string `json:"role" binding:"required" enums:"manager,member"` // допустимость проверяет сервис — одно правило и для бота
}

// putMember — добавить участника или изменить роль.
//
// @Summary      Добавить участника или изменить роль
// @Description  Менеджер проекта или администратор (§2). Добавить можно только активного пользователя. Понижение единственного активного менеджера — 409 LAST_MANAGER. В архивном проекте — 409 PROJECT_ARCHIVED.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        key      path      string            true  "ключ проекта"
// @Param        user_id  path      string            true  "id пользователя"
// @Param        body     body      putMemberRequest  true  "роль"
// @Success      200      {object}  MemberView        "роль изменена или не менялась"
// @Success      201      {object}  MemberView        "участник добавлен"
// @Failure      400      {object}  apierr.Response   "VALIDATION_FAILED, в том числе пользователь деактивирован"
// @Failure      401      {object}  apierr.Response
// @Failure      403      {object}  apierr.Response   "FORBIDDEN: не менеджер"
// @Failure      404      {object}  apierr.Response   "NOT_FOUND: нет проекта или пользователя"
// @Failure      409      {object}  apierr.Response   "LAST_MANAGER, PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/members/{user_id} [put]
func (h *Handlers) putMember(c *gin.Context) {
	a, ok := h.manage(c)
	if !ok {
		return
	}
	userID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		httpserver.Abort(c, ErrUserNotFound)
		return
	}
	// Архив — 409 раньше ошибок тела, как у PATCH проекта; под блокировкой сервис проверит ещё раз.
	if err := EnsureWritable(a.Project); err != nil {
		httpserver.Abort(c, err)
		return
	}
	var req putMemberRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	m, added, err := h.svc.PutMember(c.Request.Context(), a, userID, req.Role)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	status := http.StatusOK
	if added {
		status = http.StatusCreated
	}
	c.JSON(status, m)
}

// removeMember — исключить участника.
//
// @Summary      Исключить участника
// @Description  Менеджер проекта или администратор (§2). Единственного активного менеджера — 409 LAST_MANAGER.
// @Tags         projects
// @Security     BearerAuth
// @Param        key      path  string  true  "ключ проекта"
// @Param        user_id  path  string  true  "id пользователя"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND: нет проекта или участника"
// @Failure      409  {object}  apierr.Response  "LAST_MANAGER, PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/members/{user_id} [delete]
func (h *Handlers) removeMember(c *gin.Context) {
	a, ok := h.manage(c)
	if !ok {
		return
	}
	userID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		httpserver.Abort(c, ErrNotMember)
		return
	}
	if err := h.svc.RemoveMember(c.Request.Context(), a, userID); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func badQuery(name, reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: name, Reason: reason}})
}

// timeQuery — момент из RFC 3339 или дата ГГГГ-ММ-ДД (начало дня UTC; для to — начало следующего,
// чтобы to=2026-10-10 включало весь день).
func timeQuery(c *gin.Context, name string, endOfDay bool) (*time.Time, error) {
	v := c.Query(name)
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t, nil
	}
	d, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return nil, badQuery(name, "дата ГГГГ-ММ-ДД или момент RFC 3339")
	}
	if endOfDay {
		d = d.AddDate(0, 0, 1)
	}
	return &d, nil
}

// auditLog — журнал действий проекта.
//
// @Summary      Журнал проекта
// @Description  Действия менеджеров и администраторов (ТЗ §3.8): участники и роли, настройки, архив, удаление и восстановление. Только администратор системы. Новые записи первыми; следующая страница — cursor из next_cursor.
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        key      path      string  true   "ключ проекта"
// @Param        from     query     string  false  "с (ГГГГ-ММ-ДД или RFC 3339)"
// @Param        to       query     string  false  "по (ГГГГ-ММ-ДД включительно или RFC 3339 не включая)"
// @Param        user_id  query     string  false  "кто действовал"
// @Param        action   query     string  false  "тип действия, например member.added"
// @Param        limit    query     int     false  "до 200, по умолчанию 50"
// @Param        cursor   query     string  false  "курсор следующей страницы"
// @Success      200      {object}  audit.Page
// @Failure      400      {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401      {object}  apierr.Response
// @Failure      403      {object}  apierr.Response  "FORBIDDEN: не администратор системы"
// @Failure      404      {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/projects/{key}/audit [get]
func (h *Handlers) auditLog(c *gin.Context) {
	a, ok := h.access(c)
	if !ok {
		return
	}
	var f audit.Filter
	var err error
	if f.From, err = timeQuery(c, "from", false); err == nil {
		f.To, err = timeQuery(c, "to", true)
	}
	if err == nil && c.Query("user_id") != "" {
		id, perr := uuid.Parse(c.Query("user_id"))
		if perr != nil {
			err = badQuery("user_id", "ожидается UUID")
		}
		f.ActorID = &id
	}
	if err == nil && c.Query("limit") != "" {
		f.Limit, err = strconv.Atoi(c.Query("limit"))
		if err != nil || f.Limit < 1 || f.Limit > audit.MaxLimit {
			err = badQuery("limit", "от 1 до 200")
		}
	}
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	f.Action, f.Cursor = c.Query("action"), c.Query("cursor")
	page, err := audit.List(c.Request.Context(), h.svc.db, a.Project.ID, f)
	if errors.Is(err, audit.ErrBadCursor) {
		err = badQuery("cursor", "некорректный курсор")
	}
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
