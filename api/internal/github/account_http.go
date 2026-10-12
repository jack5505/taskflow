package github

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// nonceCookie — nonce браузера, начавшего привязку: callback принимается только в нём (§12.5).
const nonceCookie = "tf_github_nonce"

// AccountHandlers — связка GitHub-аккаунта в профиле (§7.8, §12.5).
type AccountHandlers struct {
	accounts     *Accounts
	requireAuth  gin.HandlerFunc
	secureCookie bool
	login        bool
}

// NewAccountHandlers — login=false (нет Client ID App): привязать нельзя, отвязать — можно всегда.
func NewAccountHandlers(accounts *Accounts, requireAuth gin.HandlerFunc, secureCookie, login bool) *AccountHandlers {
	return &AccountHandlers{accounts: accounts, requireAuth: requireAuth, secureCookie: secureCookie, login: login}
}

func (h *AccountHandlers) Register(api *gin.RouterGroup) {
	api.DELETE("/me/github", h.requireAuth, h.unlink)
	if h.login {
		api.GET("/me/github/connect", h.requireAuth, h.connect)
		api.GET(CallbackPath, h.callback)
	}
}

func (h *AccountHandlers) setNonce(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode) // возврат с github.com — переход верхнего уровня, Lax его пропускает
	c.SetCookie(nonceCookie, value, maxAge, "/api/v1"+CallbackPath, "", h.secureCookie, true)
}

// ConnectLink — ссылка входа в GitHub.
type ConnectLink struct {
	URL string `json:"url" example:"https://github.com/login/oauth/authorize?client_id=Iv1.abc&state=..."`
}

// connect — ссылка входа в GitHub для связки аккаунта.
//
// @Summary      Ссылка привязки GitHub
// @Description  Ссылка на вход в GitHub (авторизация пользователя GitHub App, §12.5), действует 10 минут. После входа GitHub возвращает пользователя на /api/v1/github/callback, затем в профиль. Маршрут есть, только если заданы GITHUB_APP_ID и GITHUB_CLIENT_ID.
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  github.ConnectLink
// @Failure      401  {object}  apierr.Response
// @Router       /api/v1/me/github/connect [get]
func (h *AccountHandlers) connect(c *gin.Context) {
	link, nonce := h.accounts.ConnectURL(auth.CurrentUser(c).ID)
	h.setNonce(c, nonce, int(stateTTL.Seconds()))
	c.JSON(http.StatusOK, ConnectLink{URL: link})
}

// unlink — снять связку GitHub.
//
// @Summary      Отвязать GitHub
// @Description  Снимает связку GitHub-аккаунта (§12.5); не связан — тоже 204.
// @Tags         me
// @Security     BearerAuth
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Router       /api/v1/me/github [delete]
func (h *AccountHandlers) unlink(c *gin.Context) {
	if err := h.accounts.Unlink(c.Request.Context(), auth.CurrentUser(c).ID); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// callback — возврат с github.com после входа.
//
// @Summary      Возврат из GitHub
// @Description  Сюда GitHub возвращает браузер после входа (Callback URL App). Пользователь берётся из подписанного state, а state принимается только в браузере, который получил ссылку (nonce в cookie), один раз. Итог — переадресация в профиль: /profile?github=linked или /profile?github=error&reason=state|taken|inactive|failed.
// @Tags         me
// @Param        code   query  string  true  "код авторизации GitHub"
// @Param        state  query  string  true  "state из ссылки привязки"
// @Success      302
// @Router       /api/v1/github/callback [get]
func (h *AccountHandlers) callback(c *gin.Context) {
	nonce, _ := c.Cookie(nonceCookie)
	h.setNonce(c, "", -1) // одна попытка на ссылку
	q := url.Values{}
	_, err := h.accounts.Complete(c.Request.Context(), c.Query("code"), c.Query("state"), nonce)
	switch {
	case err == nil:
		q.Set("github", "linked")
	case errors.Is(err, ErrBadState):
		q.Set("github", "error")
		q.Set("reason", "state")
	case errors.Is(err, errGitHubTaken):
		q.Set("github", "error")
		q.Set("reason", "taken")
	case errors.Is(err, errInactive):
		q.Set("github", "error")
		q.Set("reason", "inactive")
	default:
		slog.ErrorContext(c.Request.Context(), "github: account link", "err", err)
		q.Set("github", "error")
		q.Set("reason", "failed")
	}
	c.Redirect(http.StatusFound, h.accounts.baseURL+"/profile?"+q.Encode())
}
