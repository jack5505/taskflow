package report

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// Handlers — отчёты по времени (§9.2).
type Handlers struct {
	svc         *Service
	requireAuth gin.HandlerFunc
}

func NewHandlers(svc *Service, requireAuth gin.HandlerFunc) *Handlers {
	return &Handlers{svc: svc, requireAuth: requireAuth}
}

func (h *Handlers) Register(api *gin.RouterGroup) {
	api.GET("/reports/time", h.requireAuth, h.timeReport)
}

type timeQuery struct {
	From     string `form:"from" json:"from" binding:"max=10"`
	To       string `form:"to" json:"to" binding:"max=10"`
	Project  string `form:"project" json:"project" binding:"max=10"`
	SprintID string `form:"sprint_id" json:"sprint_id" binding:"omitempty,uuid"`
	UserID   string `form:"user_id" json:"user_id" binding:"omitempty,uuid"`
	GroupBy  string `form:"group_by" json:"group_by" binding:"omitempty,oneof=user task"`
	Format   string `form:"format" json:"format" binding:"omitempty,oneof=json csv"`
}

func optUUID(s string) *uuid.UUID {
	if s == "" {
		return nil
	}
	id := uuid.MustParse(s) // binding uuid уже проверил
	return &id
}

// timeReport — отчёт по времени.
//
// @Summary      Отчёт по времени
// @Description  ТЗ §6.6. Период from–to включительно (ГГГГ-ММ-ДД), по умолчанию текущая неделя пн–вс по поясу пользователя, не длиннее 366 дней. Фильтры: project (ключ), sprint_id (задачи, которые сейчас в спринте), user_id.
// @Description  group_by=user — строка на пользователя, столбец на день (days, weekend — выходные), итоги по строке (total_minutes) и по столбцу (day_totals). group_by=task — строка на задачу: ключ, название, исполнитель, статус, оценка, списано за период и всего (всего — в пределах того же фильтра по пользователю и прав); deleted — удалённая задача (§4).
// @Description  Права (§2): администратор системы — все проекты; менеджер — все записи своих проектов; исполнитель — только свои записи (user_id другого — 403). Чужой проект или спринт — 404. Удалённые проекты в отчёт не входят, архивные — входят.
// @Description  format=csv — файл для русского Excel: UTF-8 с BOM, разделитель «;», часы с десятичной запятой (2,50), даты dd.mm.yyyy, итоговая строка «Итого».
// @Tags         reports
// @Produce      json
// @Produce      text/csv
// @Security     BearerAuth
// @Param        from       query     string  false  "с (ГГГГ-ММ-ДД)"
// @Param        to         query     string  false  "по (ГГГГ-ММ-ДД, включительно)"
// @Param        project    query     string  false  "ключ проекта"
// @Param        sprint_id  query     string  false  "id спринта"
// @Param        user_id    query     string  false  "id пользователя"
// @Param        group_by   query     string  false  "представление"  Enums(user, task)
// @Param        format     query     string  false  "формат ответа"  Enums(json, csv)
// @Success      200        {object}  Report
// @Failure      400        {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401        {object}  apierr.Response
// @Failure      403        {object}  apierr.Response  "FORBIDDEN: исполнитель запросил другого пользователя"
// @Failure      404        {object}  apierr.Response  "NOT_FOUND: проект или спринт"
// @Router       /api/v1/reports/time [get]
func (h *Handlers) timeReport(c *gin.Context) {
	var q timeQuery
	if !httpserver.BindQuery(c, &q) {
		return
	}
	r, err := h.svc.Time(c.Request.Context(), auth.CurrentUser(c), Query{From: q.From, To: q.To, Project: q.Project,
		SprintID: optUUID(q.SprintID), UserID: optUUID(q.UserID), GroupBy: q.GroupBy})
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	if q.Format != "csv" {
		c.JSON(http.StatusOK, r)
		return
	}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, r); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+FileName(r)+`"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}
