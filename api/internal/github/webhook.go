package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// Заголовки вебхука GitHub.
const (
	SignatureHeader = "X-Hub-Signature-256"
	DeliveryHeader  = "X-GitHub-Delivery"
	EventHeader     = "X-GitHub-Event"
)

// maxPayloadBytes — предел тела: GitHub не шлёт вебхуки больше 25 МБ.
const maxPayloadBytes = 25 << 20

// Webhook — приём POST /api/v1/github/webhook.
type Webhook struct {
	secret []byte
	intake *Intake
}

func NewWebhook(secret string, intake *Intake) *Webhook {
	return &Webhook{secret: []byte(secret), intake: intake}
}

func (h *Webhook) Register(api *gin.RouterGroup) {
	api.POST(WebhookPath, h.receive)
}

// receive — приём доставки вебхука GitHub App.
//
// @Summary      Webhook GitHub App
// @Description  Приём событий GitHub App (§12.2). Подпись X-Hub-Signature-256 (HMAC-SHA256 тела секретом App) проверяется на каждом запросе, неверная — 401. Ответ 202 отдаётся сразу, обработка — в очереди; повторная доставка с тем же X-GitHub-Delivery (7 дней) не обрабатывается. Маршрут есть, только если задан GITHUB_APP_ID.
// @Tags         github
// @Accept       json
// @Produce      json
// @Param        X-Hub-Signature-256  header  string  true  "sha256=<hex HMAC тела>"
// @Param        X-GitHub-Delivery    header  string  true  "id доставки"
// @Param        X-GitHub-Event       header  string  true  "тип события"
// @Param        body  body      object  true  "payload события GitHub"
// @Success      202   {object}  map[string]bool  "accepted: true"
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED — нет заголовка доставки или события"
// @Failure      401   {object}  apierr.Response  "неверная подпись"
// @Failure      413   {object}  apierr.Response  "PAYLOAD_TOO_LARGE"
// @Router       /api/v1/github/webhook [post]
func (h *Webhook) receive(c *gin.Context) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxPayloadBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpserver.Abort(c, apierr.PayloadTooLarge("Тело вебхука больше 25 МБ"))
			return
		}
		httpserver.Abort(c, apierr.BadRequest(apierr.CodeInvalidJSON, "Тело запроса не прочитано"))
		return
	}
	if !validSignature(h.secret, body, c.GetHeader(SignatureHeader)) {
		httpserver.Abort(c, apierr.Unauthorized("Неверная подпись вебхука"))
		return
	}
	delivery, event := c.GetHeader(DeliveryHeader), c.GetHeader(EventHeader)
	if delivery == "" || event == "" {
		httpserver.Abort(c, apierr.BadRequest(apierr.CodeValidation, "Нужны заголовки X-GitHub-Delivery и X-GitHub-Event").
			With("fields", []apierr.FieldError{{Field: DeliveryHeader, Reason: "id доставки и тип события обязательны"}}))
		return
	}
	if _, err := h.intake.Accept(c.Request.Context(), event, delivery, body); err != nil {
		httpserver.Abort(c, err) // 500: GitHub повторит доставку
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"accepted": true})
}

// validSignature — header = "sha256=" + hex(HMAC-SHA256(body, secret)), сравнение за постоянное время.
func validSignature(secret, body []byte, header string) bool {
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}
