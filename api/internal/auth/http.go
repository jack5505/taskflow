package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/user"
)

var forUpdate = clause.Locking{Strength: "UPDATE"}

const (
	// RefreshCookie — httpOnly cookie с refresh-токеном (ТЗ §9.1). Path ограничивает её маршрутами auth.
	RefreshCookie     = "refresh_token"
	refreshCookiePath = "/api/v1/auth"
	// csrfHeader — защита от CSRF для cookie (ТЗ §10.3): браузер не отправит такой заголовок
	// с чужого сайта без CORS-preflight, поэтому запрос с cookie обязан его нести.
	csrfHeader = "X-Requested-With"
)

// Handlers — HTTP-обработчики входа. secureCookie=true на HTTPS (production).
type Handlers struct {
	svc          *Service
	secureCookie bool
	refreshTTL   int
}

func NewHandlers(svc *Service, secureCookie bool) *Handlers {
	return &Handlers{svc: svc, secureCookie: secureCookie, refreshTTL: int(svc.refreshTTL.Seconds())}
}

// Register добавляет маршруты /auth/* в группу /api/v1.
func (h *Handlers) Register(api *gin.RouterGroup) {
	g := api.Group("/auth")
	g.POST("/login", h.login)
	g.POST("/refresh", h.refresh)
	g.POST("/logout", h.logout)
	g.POST("/logout-all", h.RequireAuth(), h.logoutAll)
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,max=200"`
}

// SessionResponse — ответ на вход и обновление.
type SessionResponse struct {
	AccessToken string     `json:"access_token"`
	TokenType   string     `json:"token_type" example:"Bearer"`
	ExpiresIn   int        `json:"expires_in" example:"900"`
	User        *user.User `json:"user"`
}

// login — вход по email и паролю.
//
// @Summary      Вход
// @Description  Возвращает access-токен (15 минут) и ставит httpOnly cookie refresh_token (30 дней). После 10 неудачных попыток вход по email закрыт на 15 минут.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      loginRequest  true  "email и пароль"
// @Success      200   {object}  SessionResponse
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response  "UNAUTHORIZED: неверный email или пароль, пользователь деактивирован"
// @Failure      429   {object}  apierr.Response  "RATE_LIMITED: вход заблокирован, details.retry_after_seconds"
// @Router       /api/v1/auth/login [post]
func (h *Handlers) login(c *gin.Context) {
	var req loginRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	s, err := h.svc.Login(c.Request.Context(), req.Email, req.Password, c.Request.UserAgent())
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	h.respond(c, s)
}

// refresh — новая пара токенов по cookie; старый refresh отзывается.
//
// @Summary      Обновить access-токен
// @Description  Требует cookie refresh_token и заголовок X-Requested-With: XMLHttpRequest (защита от CSRF). Старый refresh-токен отзывается.
// @Tags         auth
// @Produce      json
// @Success      200  {object}  SessionResponse
// @Failure      401  {object}  apierr.Response  "UNAUTHORIZED: сессия истекла или отозвана"
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: нет заголовка X-Requested-With"
// @Router       /api/v1/auth/refresh [post]
func (h *Handlers) refresh(c *gin.Context) {
	token, ok := h.cookieToken(c)
	if !ok {
		return
	}
	s, err := h.svc.Refresh(c.Request.Context(), token, c.Request.UserAgent())
	if err != nil {
		h.clearCookie(c)
		httpserver.Abort(c, err)
		return
	}
	h.respond(c, s)
}

// logout — отзыв текущей сессии.
//
// @Summary      Выйти
// @Description  Отзывает refresh-токен из cookie и очищает её. Требует X-Requested-With: XMLHttpRequest.
// @Tags         auth
// @Success      204
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: нет заголовка X-Requested-With"
// @Router       /api/v1/auth/logout [post]
func (h *Handlers) logout(c *gin.Context) {
	if c.GetHeader(csrfHeader) == "" {
		httpserver.Abort(c, apierr.Forbidden("Нет заголовка "+csrfHeader))
		return
	}
	if token, err := c.Cookie(RefreshCookie); err == nil && token != "" {
		if err := h.svc.Logout(c.Request.Context(), token); err != nil {
			httpserver.Abort(c, err)
			return
		}
	}
	h.clearCookie(c)
	c.Status(http.StatusNoContent)
}

// logoutAll — «Выйти везде»: отзыв всех сессий пользователя (ТЗ §7.8, §9.1).
//
// @Summary      Выйти везде
// @Tags         auth
// @Security     BearerAuth
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Router       /api/v1/auth/logout-all [post]
func (h *Handlers) logoutAll(c *gin.Context) {
	u := CurrentUser(c)
	if err := h.svc.RevokeAll(c.Request.Context(), u.ID); err != nil {
		httpserver.Abort(c, err)
		return
	}
	h.clearCookie(c)
	c.Status(http.StatusNoContent)
}

func (h *Handlers) cookieToken(c *gin.Context) (string, bool) {
	if c.GetHeader(csrfHeader) == "" {
		httpserver.Abort(c, apierr.Forbidden("Нет заголовка "+csrfHeader))
		return "", false
	}
	token, err := c.Cookie(RefreshCookie)
	if err != nil || token == "" {
		httpserver.Abort(c, ErrBadRefresh)
		return "", false
	}
	return token, true
}

func (h *Handlers) respond(c *gin.Context, s *Session) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(RefreshCookie, s.RefreshToken, h.refreshTTL, refreshCookiePath, "", h.secureCookie, true)
	c.JSON(http.StatusOK, SessionResponse{AccessToken: s.AccessToken, TokenType: "Bearer", ExpiresIn: s.ExpiresIn, User: s.User})
}

func (h *Handlers) clearCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(RefreshCookie, "", -1, refreshCookiePath, "", h.secureCookie, true)
}

const ctxUser = "auth.user"

// RequireAuth — middleware: Bearer access-токен обязателен, пользователь активен.
func (h *Handlers) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			httpserver.Abort(c, ErrNoToken)
			return
		}
		u, err := h.svc.Authenticate(c.Request.Context(), token)
		if err != nil {
			var e *apierr.Error
			if !errors.As(err, &e) {
				err = apierr.Internal(err)
			}
			httpserver.Abort(c, err)
			return
		}
		c.Set(ctxUser, u)
		c.Next()
	}
}

// CurrentUser — пользователь, прошедший RequireAuth. Вне защищённого маршрута — nil.
func CurrentUser(c *gin.Context) *user.User {
	if u, ok := c.Get(ctxUser); ok {
		return u.(*user.User)
	}
	return nil
}
