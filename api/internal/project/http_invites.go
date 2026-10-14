package project

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// SessionResponder — ответ входа с cookie refresh_token (auth.Handlers.Respond): регистрация по
// ссылке сразу входит в систему.
type SessionResponder interface {
	Respond(c *gin.Context, s *auth.Session)
}

// WithInvites подключает маршруты ссылок-приглашений (§7.1): сервис должен быть собран с
// Service.WithInvites.
func (h *Handlers) WithInvites(resp SessionResponder) *Handlers {
	h.sessions = resp
	return h
}

// registerInvites — управление ссылками (менеджер) и страница приглашения (без входа).
func (h *Handlers) registerInvites(api *gin.RouterGroup) {
	g := api.Group("/projects", h.requireAuth)
	g.GET("/:key/invites", h.invites)
	g.POST("/:key/invites", h.createInvite)
	g.DELETE("/:key/invites/:id", h.revokeInvite)
	inv := api.Group("/invites")
	inv.GET("/:token", h.inviteInfo)
	inv.POST("/:token/register", h.registerByInvite)
	inv.POST("/:token/accept", h.requireAuth, h.acceptInvite)
}

// invites — действующие ссылки-приглашения проекта.
//
// @Summary      Ссылки-приглашения проекта
// @Description  Неотозванные и неистёкшие ссылки, новые первыми (§7.1, §7.9). Токен не возвращается: полная ссылка есть только в ответе на создание.
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        key  path      string  true  "ключ проекта"
// @Success      200  {array}   InviteView
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404  {object}  apierr.Response
// @Router       /api/v1/projects/{key}/invites [get]
func (h *Handlers) invites(c *gin.Context) {
	a, ok := h.manage(c)
	if !ok {
		return
	}
	list, err := h.svc.Invites(c.Request.Context(), a)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

type createInviteRequest struct {
	Role string `json:"role" binding:"required,oneof=manager member" enums:"manager,member"`
}

// createInvite — новая ссылка-приглашение.
//
// @Summary      Создать ссылку-приглашение
// @Description  Ссылка с ролью в проекте, действует 7 дней или до отзыва; по ней может присоединиться любое число людей (§7.1). Полная ссылка (url) возвращается только здесь.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        key   path      string               true  "ключ проекта"
// @Param        body  body      createInviteRequest  true  "роль"
// @Success      201   {object}  CreatedInvite
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Failure      403   {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404   {object}  apierr.Response
// @Failure      409   {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/invites [post]
func (h *Handlers) createInvite(c *gin.Context) {
	a, ok := h.manage(c)
	if !ok {
		return
	}
	if err := EnsureWritable(a.Project); err != nil {
		httpserver.Abort(c, err)
		return
	}
	var req createInviteRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	inv, err := h.svc.CreateInvite(c.Request.Context(), a, req.Role)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, inv)
}

// revokeInvite — отозвать ссылку.
//
// @Summary      Отозвать ссылку-приглашение
// @Description  По отозванной ссылке больше никто не присоединится; уже вступившие остаются в проекте.
// @Tags         projects
// @Security     BearerAuth
// @Param        key  path  string  true  "ключ проекта"
// @Param        id   path  string  true  "id ссылки"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: не менеджер"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND: нет проекта или действующей ссылки"
// @Failure      409  {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/projects/{key}/invites/{id} [delete]
func (h *Handlers) revokeInvite(c *gin.Context) {
	a, ok := h.manage(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpserver.Abort(c, ErrInviteNotFound)
		return
	}
	if err := h.svc.RevokeInvite(c.Request.Context(), a, id); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// inviteInfo — страница приглашения: проект и роль.
//
// @Summary      Приглашение по ссылке
// @Description  Без входа. Ссылка отозвана, истекла, проект в архиве или удалён — 404 (§7.1).
// @Tags         invites
// @Produce      json
// @Param        token  path      string  true  "токен из ссылки"
// @Success      200    {object}  InviteInfo
// @Failure      404    {object}  apierr.Response  "NOT_FOUND: ссылка недействительна"
// @Router       /api/v1/invites/{token} [get]
func (h *Handlers) inviteInfo(c *gin.Context) {
	info, err := h.svc.InviteInfo(c.Request.Context(), c.Param("token"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, info)
}

type registerRequest struct {
	Email       string `json:"email" binding:"required,email,max=254"`
	DisplayName string `json:"display_name" binding:"required,max=200"`
	Password    string `json:"password" binding:"required,min=10,max=72"`
}

// registerByInvite — регистрация по ссылке.
//
// @Summary      Зарегистрироваться по ссылке-приглашению
// @Description  Без входа. Создаёт пользователя, делает его участником проекта с ролью из ссылки и входит: ответ и cookie refresh_token — как у /auth/login (§7.1). Email письмом не подтверждается. Занятый email — 400 по полю email. Не больше 10 попыток в час с одного IP.
// @Tags         invites
// @Accept       json
// @Produce      json
// @Param        token  path      string           true  "токен из ссылки"
// @Param        body   body      registerRequest  true  "имя, email, пароль (не короче 10 символов)"
// @Success      200    {object}  auth.SessionResponse
// @Failure      400    {object}  apierr.Response  "VALIDATION_FAILED, в том числе email занят"
// @Failure      404    {object}  apierr.Response  "NOT_FOUND: ссылка недействительна"
// @Failure      429    {object}  apierr.Response  "RATE_LIMITED: details.retry_after_seconds"
// @Router       /api/v1/invites/{token}/register [post]
func (h *Handlers) registerByInvite(c *gin.Context) {
	var req registerRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	s, err := h.svc.Register(c.Request.Context(), c.Param("token"), RegisterInput{
		Email: req.Email, DisplayName: req.DisplayName, Password: req.Password,
		IP: c.ClientIP(), UserAgent: c.Request.UserAgent(),
	})
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	h.sessions.Respond(c, s)
}

// acceptInvite — вошедший пользователь присоединяется к проекту.
//
// @Summary      Присоединиться к проекту по ссылке
// @Description  Уже участник — роль не меняется (added=false).
// @Tags         invites
// @Produce      json
// @Security     BearerAuth
// @Param        token  path      string  true  "токен из ссылки"
// @Success      200    {object}  JoinResult
// @Failure      401    {object}  apierr.Response
// @Failure      403    {object}  apierr.Response  "FORBIDDEN: пользователя исключили из проекта после создания ссылки"
// @Failure      404    {object}  apierr.Response  "NOT_FOUND: ссылка недействительна"
// @Router       /api/v1/invites/{token}/accept [post]
func (h *Handlers) acceptInvite(c *gin.Context) {
	res, err := h.svc.AcceptInvite(c.Request.Context(), c.Param("token"), auth.CurrentUser(c))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}
