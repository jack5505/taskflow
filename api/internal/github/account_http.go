package github

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// AccountHandlers — связка GitHub-аккаунта в профиле (§7.8, §12.5).
type AccountHandlers struct {
	accounts    *Accounts
	requireAuth gin.HandlerFunc
}

func NewAccountHandlers(accounts *Accounts, requireAuth gin.HandlerFunc) *AccountHandlers {
	return &AccountHandlers{accounts: accounts, requireAuth: requireAuth}
}

func (h *AccountHandlers) Register(api *gin.RouterGroup) {
	api.GET("/me/github/connect", h.requireAuth, h.connect)
	api.DELETE("/me/github", h.requireAuth, h.unlink)
	api.GET(CallbackPath, h.callback)
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
	c.JSON(http.StatusOK, ConnectLink{URL: h.accounts.ConnectURL(auth.CurrentUser(c).ID)})
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
// @Description  Сюда GitHub возвращает браузер после входа (Callback URL App). Пользователь берётся из подписанного state, не из сессии. Итог — переадресация в профиль: /profile?github=linked или /profile?github=error&message=<текст>.
// @Tags         me
// @Param        code   query  string  true  "код авторизации GitHub"
// @Param        state  query  string  true  "state из ссылки привязки"
// @Success      302
// @Router       /api/v1/github/callback [get]
func (h *AccountHandlers) callback(c *gin.Context) {
	q := url.Values{}
	if _, err := h.accounts.Complete(c.Request.Context(), c.Query("code"), c.Query("state")); err != nil {
		q.Set("github", "error")
		var ae *apierr.Error
		if errors.As(err, &ae) && ae.Status < 500 {
			q.Set("message", ae.Message)
		} else {
			slog.ErrorContext(c.Request.Context(), "github: account link", "err", err)
			q.Set("message", "GitHub не ответил — попробуйте ещё раз")
		}
	} else {
		q.Set("github", "linked")
	}
	c.Redirect(http.StatusFound, h.accounts.baseURL+"/profile?"+q.Encode())
}
