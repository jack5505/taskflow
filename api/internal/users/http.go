package users

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// Handlers — /api/v1/users (ТЗ §9.2).
type Handlers struct {
	svc         *Service
	requireAuth gin.HandlerFunc
}

func NewHandlers(svc *Service, requireAuth gin.HandlerFunc) *Handlers {
	return &Handlers{svc: svc, requireAuth: requireAuth}
}

// Register добавляет маршруты в группу /api/v1.
func (h *Handlers) Register(api *gin.RouterGroup) {
	g := api.Group("/users", h.requireAuth)
	g.GET("", h.list)
	g.POST("", auth.RequireSystemAdmin(), h.create)
	g.PATCH("/:id", auth.RequireSystemAdmin(), h.update)
}

// list — список пользователей.
//
// @Summary      Пользователи
// @Description  Администратору — все пользователи целиком (user.User). Менеджеру проекта — только активные и только id, email, display_name (user.Brief) — для добавления в проект.
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   user.User  "администратору; менеджеру — массив user.Brief"
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не администратор и не менеджер"
// @Router       /api/v1/users [get]
func (h *Handlers) list(c *gin.Context) {
	ok, err := h.svc.CanList(c.Request.Context(), auth.CurrentUser(c))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	if !ok {
		httpserver.Abort(c, ErrForbiddenList)
		return
	}
	if !auth.CurrentUser(c).IsSystemAdmin {
		brief, err := h.svc.ListBrief(c.Request.Context())
		if err != nil {
			httpserver.Abort(c, err)
			return
		}
		c.JSON(http.StatusOK, brief)
		return
	}
	list, err := h.svc.ListAll(c.Request.Context())
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

type createRequest struct {
	Email         string `json:"email" binding:"required,email,max=254"`
	DisplayName   string `json:"display_name" binding:"required,max=200"` // 1–80 после обрезки пробелов проверяет сервис
	Timezone      string `json:"timezone" binding:"max=64" example:"Europe/Moscow"`
	IsSystemAdmin bool   `json:"is_system_admin"`
}

// create — новый пользователь; на email уходит ссылка установки пароля (24 часа).
//
// @Summary      Создать пользователя
// @Description  Пользователь создаётся без пароля, на email уходит письмо со ссылкой установки пароля (ТЗ §7.1). timezone по умолчанию Europe/Moscow.
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      createRequest  true  "пользователь"
// @Success      201   {object}  user.User
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED, в том числе email уже используется"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: не администратор системы"
// @Router       /api/v1/users [post]
func (h *Handlers) create(c *gin.Context) {
	var req createRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	u, err := h.svc.Create(c.Request.Context(), CreateInput(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, u)
}

type updateRequest struct {
	DisplayName   *string `json:"display_name" binding:"omitempty,max=200"`
	Timezone      *string `json:"timezone" binding:"omitempty,max=64"`
	IsSystemAdmin *bool   `json:"is_system_admin"`
	IsActive      *bool   `json:"is_active"`
}

// update — изменение и деактивация пользователя.
//
// @Summary      Изменить пользователя
// @Description  Меняются только переданные поля. Последнего активного администратора системы нельзя деактивировать или лишить флага (400, поле is_active / is_system_admin). is_active=false — деактивация (ТЗ §3.1): сессии отзываются, привязка Telegram снимается; если пользователь — единственный активный менеджер проекта, 409 LAST_MANAGER со списком проектов в details.projects.
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "id пользователя"
// @Param        body  body      updateRequest  true  "поля для изменения"
// @Success      200   {object}  user.User
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: не администратор системы"
// @Failure      404   {object}  apierr.Response  "NOT_FOUND"
// @Failure      409   {object}  apierr.Response  "LAST_MANAGER"
// @Router       /api/v1/users/{id} [patch]
func (h *Handlers) update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpserver.Abort(c, ErrNotFound)
		return
	}
	var req updateRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	u, err := h.svc.Update(c.Request.Context(), id, UpdateInput(req))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}
