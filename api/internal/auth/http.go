package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/user"
)

const (
	// RefreshCookie — httpOnly cookie с refresh-токеном (ТЗ §9.1). Path ограничивает её маршрутами auth.
	RefreshCookie     = "refresh_token"
	refreshCookiePath = "/api/v1/auth"
	// csrfHeader — защита от CSRF для cookie (ТЗ §10.3): браузер не отправит такой заголовок
	// с чужого сайта без CORS-preflight, поэтому запрос с cookie обязан его нести.
	csrfHeader = "X-Requested-With"
)

// Handlers — HTTP-обработчики входа. secureCookie — флаг Secure у cookie; берётся из схемы
// APP_BASE_URL (SecureCookies), а не из APP_ENV: стенд работает по HTTPS и не в production.
type Handlers struct {
	svc          *Service
	secureCookie bool
	refreshTTL   int
}

func NewHandlers(svc *Service, secureCookie bool) *Handlers {
	return &Handlers{svc: svc, secureCookie: secureCookie, refreshTTL: int(svc.refreshTTL.Seconds())}
}

// SecureCookies сообщает, ставить ли Secure на cookie: да, если приложение открыто по HTTPS.
func SecureCookies(baseURL string) bool {
	return strings.HasPrefix(strings.ToLower(baseURL), "https://")
}

// Register добавляет маршруты /auth/* в группу /api/v1.
func (h *Handlers) Register(api *gin.RouterGroup) {
	g := api.Group("/auth")
	g.POST("/login", h.login)
	g.POST("/refresh", h.refresh)
	g.POST("/logout", h.logout)
	g.POST("/logout-all", h.RequireAuth(), h.logoutAll)
	g.POST("/password-reset", h.passwordReset)
	g.POST("/password-set", h.passwordSet)
	g.POST("/password-change", h.RequireAuth(), h.passwordChange)
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
// @Param        X-Requested-With  header    string  true  "Защита от CSRF: любое значение, например XMLHttpRequest"
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
		// Cookie стираем только при отказе по существу (401/403): при сбое базы токен ещё жив,
		// и клиент повторит запрос.
		if apierr.From(err).Status < http.StatusInternalServerError {
			h.clearCookie(c)
		}
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
// @Param        X-Requested-With  header    string  true  "Защита от CSRF: любое значение, например XMLHttpRequest"
// @Success      204
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: нет заголовка X-Requested-With"
// @Router       /api/v1/auth/logout [post]
func (h *Handlers) logout(c *gin.Context) {
	if !requireCSRFHeader(c) {
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

type passwordResetRequest struct {
	Email string `json:"email" binding:"required,email,max=254"`
}

type passwordSetRequest struct {
	Token    string `json:"token" binding:"required,max=200"`
	Password string `json:"password" binding:"required,min=10,max=72"`
}

// passwordReset — письмо со ссылкой восстановления пароля (ТЗ §7.1, §9.1).
//
// @Summary      Восстановить пароль
// @Description  Отправляет на email ссылку для установки нового пароля (действует 24 часа, одноразовая). Ответ всегда 204, даже если такого пользователя нет; повторный запрос на тот же email в течение минуты письма не шлёт.
// @Tags         auth
// @Accept       json
// @Param        body  body  passwordResetRequest  true  "email"
// @Success      204
// @Failure      400  {object}  apierr.Response  "VALIDATION_FAILED"
// @Router       /api/v1/auth/password-reset [post]
func (h *Handlers) passwordReset(c *gin.Context) {
	var req passwordResetRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	if err := h.svc.RequestPasswordReset(c.Request.Context(), req.Email); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// passwordSet — установка пароля по ссылке из письма (ТЗ §7.1, §9.1).
//
// @Summary      Задать пароль по ссылке
// @Description  Токен из ссылки одноразовый: после успешной установки повторно не принимается. Все сессии пользователя отзываются.
// @Tags         auth
// @Accept       json
// @Param        body  body  passwordSetRequest  true  "токен из ссылки и новый пароль (не короче 10 символов)"
// @Success      204
// @Failure      400  {object}  apierr.Response  "VALIDATION_FAILED: пароль короче 10 символов; details.fields[token] — ссылка недействительна, использована или истекла"
// @Router       /api/v1/auth/password-set [post]
func (h *Handlers) passwordSet(c *gin.Context) {
	var req passwordSetRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	if err := h.svc.SetPassword(c.Request.Context(), req.Token, req.Password); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password" binding:"required,max=200"`
	NewPassword     string `json:"new_password" binding:"required,max=200"`
}

// passwordChange — смена пароля в профиле с вводом текущего (ТЗ §7.8, §9.1).
//
// @Summary      Сменить пароль
// @Description  Новый пароль — по тем же правилам, что при установке: не короче 10 символов и не длиннее 72 байт. После смены отзываются все сессии пользователя, кроме текущей — той, чей refresh-токен пришёл в cookie refresh_token (без cookie отзываются все). Неверный текущий пароль засчитывается в счётчик: после 10 неудач за 15 минут смена пароля закрыта на 15 минут, даже с верным.
// @Tags         auth
// @Accept       json
// @Security     BearerAuth
// @Param        body  body  passwordChangeRequest  true  "текущий и новый пароль"
// @Success      204
// @Failure      400  {object}  apierr.Response  "VALIDATION_FAILED: details.fields[current_password] — неверный текущий пароль, details.fields[new_password] — новый не подходит по правилам"
// @Failure      401  {object}  apierr.Response
// @Failure      429  {object}  apierr.Response  "RATE_LIMITED: слишком много неверных попыток, details.retry_after_seconds"
// @Router       /api/v1/auth/password-change [post]
func (h *Handlers) passwordChange(c *gin.Context) {
	var req passwordChangeRequest
	if !httpserver.Bind(c, &req) {
		return
	}
	// Cookie с refresh-токеном приходит сюда (её Path — /api/v1/auth) и определяет текущую сессию.
	// CSRF-заголовок не нужен: запрос авторизует Bearer-токен, а не cookie.
	keep, _ := c.Cookie(RefreshCookie)
	if err := h.svc.ChangePassword(c.Request.Context(), CurrentUser(c), req.CurrentPassword, req.NewPassword, keep); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// requireCSRFHeader отклоняет запрос 403, если нет заголовка csrfHeader (см. его комментарий).
func requireCSRFHeader(c *gin.Context) bool {
	if c.GetHeader(csrfHeader) == "" {
		httpserver.Abort(c, apierr.Forbidden("Нет заголовка "+csrfHeader))
		return false
	}
	return true
}

func (h *Handlers) cookieToken(c *gin.Context) (string, bool) {
	if !requireCSRFHeader(c) {
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
			httpserver.Abort(c, err)
			return
		}
		c.Set(ctxUser, u)
		c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), u.ID))
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

// RequireSystemAdmin — middleware после RequireAuth: только администратор системы (§2).
func RequireSystemAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if u := CurrentUser(c); u == nil || !u.IsSystemAdmin {
			httpserver.Abort(c, apierr.Forbidden("Доступно только администратору системы"))
			return
		}
		c.Next()
	}
}
