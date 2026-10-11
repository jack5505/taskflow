package task

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

func (h *Handlers) registerWorklogs(api *gin.RouterGroup) {
	api.GET("/tasks/:task_key/worklogs", h.requireAuth, h.worklogs)
	api.POST("/tasks/:task_key/worklogs", h.requireAuth, h.addWorklog)
	api.PATCH("/worklogs/:id", h.requireAuth, h.updateWorklog)
	api.DELETE("/worklogs/:id", h.requireAuth, h.deleteWorklog)
}

// worklogRequest — новая запись. Значения проверяет сервис после прав (404 → 403 → 400):
// бот вызывает его напрямую.
type worklogRequest struct {
	Duration string  `json:"duration" example:"2h 30m"`                              // §6.1: d, h, m от большей к меньшей; 1d = 8h; от 1m до 24h
	WorkDate *string `json:"work_date" example:"2026-10-11" format:"date"`           // по умолчанию сегодня по поясу пользователя
	Comment  *string `json:"comment" example:"Разбор логов" extensions:"x-nullable"` // до 500 символов
}

// worklogPatchRequest — изменение записи: только переданные поля; comment: null очищает.
type worklogPatchRequest struct {
	Duration Opt[string] `json:"duration" swaggertype:"string" example:"1h 15m"`
	WorkDate Opt[string] `json:"work_date" swaggertype:"string" example:"2026-10-10"`
	Comment  Opt[string] `json:"comment" swaggertype:"string" extensions:"x-nullable"`
}

// worklogs — записи времени задачи.
//
// @Summary      Записи времени задачи
// @Description  Новые сверху (по дате работы). Участник проекта; записи удалённой задачи — администратор системы.
// @Tags         worklogs
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи"
// @Success      200       {array}   WorklogView
// @Failure      401       {object}  apierr.Response
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/tasks/{task_key}/worklogs [get]
func (h *Handlers) worklogs(c *gin.Context) {
	list, err := h.svc.Worklogs(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// addWorklog — списать время.
//
// @Summary      Списать время
// @Description  Исполнитель задачи или менеджер проекта (§2), запись — от имени текущего пользователя (source manual, через бота — telegram).
// @Description  duration — §6.1 («2h 30m», «45m», «3h», «1d» = 8h), от 1m до 24h, иначе 400 INVALID_DURATION. work_date — не позже сегодня и не раньше даты создания задачи по поясу пользователя (§3.5).
// @Description  Сумма записей пользователя за дату по всем задачам — не больше 24 часов (§6.3), иначе 409 DAILY_LIMIT_EXCEEDED (в details used_minutes и available_minutes).
// @Description  Отменённая задача и задача закрытого спринта (§4, §6.5) — 409 INVALID_TRANSITION. spent_minutes задачи пересчитывается, version не меняется. Событие worklog.created.
// @Tags         worklogs
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string          true  "ключ задачи"
// @Param        body      body      worklogRequest  true  "запись"
// @Success      201       {object}  WorklogView
// @Failure      400       {object}  apierr.Response  "INVALID_DURATION, VALIDATION_FAILED (work_date, comment), INVALID_JSON"
// @Failure      401       {object}  apierr.Response
// @Failure      403       {object}  apierr.Response  "FORBIDDEN: не исполнитель и не менеджер"
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "DAILY_LIMIT_EXCEEDED, INVALID_TRANSITION, PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key}/worklogs [post]
func (h *Handlers) addWorklog(c *gin.Context) {
	var req worklogRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	v, err := h.svc.AddWorklog(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"), WorklogInput(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, v)
}

func worklogID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpserver.Abort(c, ErrWorklogNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// updateWorklog — изменить запись времени.
//
// @Summary      Изменить запись времени
// @Description  Автор — в течение 7 календарных дней после work_date, менеджер проекта — любую без срока (§6.5). Правка чужой записи пишется в журнал аудита (§3.8).
// @Description  Ограничения полей и дневной лимит — как при создании; день и лимит — по поясу владельца записи. Задача закрытого спринта — 409 INVALID_TRANSITION. Событие worklog.updated.
// @Tags         worklogs
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string               true  "id записи"
// @Param        body  body      worklogPatchRequest  true  "изменяемые поля"
// @Success      200   {object}  WorklogView
// @Failure      400   {object}  apierr.Response  "INVALID_DURATION, VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: чужая запись или прошло 7 дней"
// @Failure      404   {object}  apierr.Response  "NOT_FOUND"
// @Failure      409   {object}  apierr.Response  "DAILY_LIMIT_EXCEEDED, INVALID_TRANSITION, PROJECT_ARCHIVED"
// @Router       /api/v1/worklogs/{id} [patch]
func (h *Handlers) updateWorklog(c *gin.Context) {
	id, ok := worklogID(c)
	if !ok {
		return
	}
	var req worklogPatchRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	v, err := h.svc.UpdateWorklog(c.Request.Context(), auth.CurrentUser(c), id, WorklogPatch(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// deleteWorklog — удалить запись времени.
//
// @Summary      Удалить запись времени
// @Description  Права и сроки — как у правки (§6.5); удаление чужой записи — в журнал аудита (§3.8). spent_minutes задачи пересчитывается. Событие worklog.deleted.
// @Tags         worklogs
// @Security     BearerAuth
// @Param        id  path  string  true  "id записи"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: чужая запись или прошло 7 дней"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Failure      409  {object}  apierr.Response  "INVALID_TRANSITION: закрытый спринт; PROJECT_ARCHIVED"
// @Router       /api/v1/worklogs/{id} [delete]
func (h *Handlers) deleteWorklog(c *gin.Context) {
	id, ok := worklogID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteWorklog(c.Request.Context(), auth.CurrentUser(c), id); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
