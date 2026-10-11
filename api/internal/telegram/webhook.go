package telegram

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// SecretHeader — заголовок, в котором Telegram передаёт secret_token из setWebhook (§8.8).
const SecretHeader = "X-Telegram-Bot-Api-Secret-Token"

// maxUpdateBytes — предел тела обновления. Обновления бота — короткие сообщения и нажатия
// кнопок (privacy mode, §8.2), мегабайта хватает с запасом.
const maxUpdateBytes = 1 << 20

// Webhook — приём обновлений POST /api/v1/telegram/webhook.
type Webhook struct {
	secret string
	intake *Intake
}

func NewWebhook(secret string, intake *Intake) *Webhook {
	return &Webhook{secret: secret, intake: intake}
}

func (h *Webhook) Register(api *gin.RouterGroup) {
	api.POST(WebhookPath, h.receive)
}

// receive — приём обновления от Telegram.
//
// @Summary      Webhook Telegram
// @Description  Приём обновлений бота (§8.8). Секрет в X-Telegram-Bot-Api-Secret-Token проверяется на каждом запросе. Ответ 200 отдаётся сразу, обработка идёт в очереди; повторно доставленный update_id (24 часа) не обрабатывается. Маршрут есть, только если задан TELEGRAM_BOT_TOKEN и бот работает в режиме webhook.
// @Tags         telegram
// @Accept       json
// @Produce      json
// @Param        X-Telegram-Bot-Api-Secret-Token  header  string  true  "секрет webhook"
// @Param        body  body      object  true  "Update из Bot API"
// @Success      200   {object}  map[string]bool  "ok: true"
// @Failure      400   {object}  apierr.Response  "INVALID_JSON"
// @Failure      401   {object}  apierr.Response  "неверный секрет"
// @Failure      413   {object}  apierr.Response  "PAYLOAD_TOO_LARGE"
// @Router       /api/v1/telegram/webhook [post]
func (h *Webhook) receive(c *gin.Context) {
	got := c.GetHeader(SecretHeader)
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.secret)) != 1 {
		httpserver.Abort(c, apierr.Unauthorized("Неверный секрет webhook"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxUpdateBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpserver.Abort(c, apierr.PayloadTooLarge("Обновление больше 1 МБ"))
			return
		}
		httpserver.Abort(c, apierr.BadRequest(apierr.CodeInvalidJSON, "Тело запроса не прочитано"))
		return
	}
	var head struct {
		UpdateID *int64 `json:"update_id"`
	}
	if err := json.Unmarshal(body, &head); err != nil || head.UpdateID == nil {
		httpserver.Abort(c, apierr.BadRequest(apierr.CodeInvalidJSON, "Ожидается Update с update_id"))
		return
	}
	if _, err := h.intake.Accept(c.Request.Context(), *head.UpdateID, body); err != nil {
		httpserver.Abort(c, err) // 500: Telegram доставит обновление повторно
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
