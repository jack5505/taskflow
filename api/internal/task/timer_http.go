package task

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

func (h *Handlers) registerTimer(api *gin.RouterGroup) {
	api.POST("/tasks/:task_key/timer/start", h.requireAuth, h.startTimer)
	api.POST("/me/timer/stop", h.requireAuth, h.stopTimer)
}

// startTimer — запустить таймер.
//
// @Summary      Запустить таймер
// @Description  Исполнитель задачи в статусе in_progress (§6.4). У пользователя один таймер: запущенный на другой задаче останавливается с записью времени (поле stopped). Повторный старт на той же задаче — 409 TIMER_ALREADY_RUNNING (в details — timer).
// @Description  Таймер останавливается сам: при уходе задачи из in_progress, смене исполнителя, удалении задачи, деактивации пользователя, архивации проекта и через 12 часов (timer.auto_stopped).
// @Tags         timer
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи"
// @Success      200       {object}  TimerStart
// @Failure      401       {object}  apierr.Response
// @Failure      403       {object}  apierr.Response  "FORBIDDEN: не исполнитель задачи"
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "TIMER_ALREADY_RUNNING, INVALID_TRANSITION: задача не в работе, PROJECT_ARCHIVED"
// @Router       /api/v1/tasks/{task_key}/timer/start [post]
func (h *Handlers) startTimer(c *gin.Context) {
	v, err := h.svc.StartTimer(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// stopTimer — остановить свой таймер.
//
// @Summary      Остановить таймер
// @Description  Запись времени source = timer, длительность округляется вверх до минуты; меньше минуты — записи нет. Через полночь по поясу пользователя — две записи на разные даты.
// @Description  Запись сверх дневного лимита 24 часа обрезается до остатка (trimmed, событие timer.trimmed), остатка нет — записи нет (§6.4).
// @Tags         timer
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  TimerStop
// @Failure      401  {object}  apierr.Response
// @Failure      404  {object}  apierr.Response  "NOT_FOUND: таймер не запущен"
// @Router       /api/v1/me/timer/stop [post]
func (h *Handlers) stopTimer(c *gin.Context) {
	v, err := h.svc.StopTimer(c.Request.Context(), auth.CurrentUser(c))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}
