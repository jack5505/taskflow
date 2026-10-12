package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
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

// maxPayloadBytes — предел тела до проверки подписи. App подписан только на pull_request и
// create (§12.1) — их тела в сотни раз меньше; предел не даёт забить память неподписанными телами.
const maxPayloadBytes = 2 << 20

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
// @Description  Приём событий GitHub App (§12.2). Подпись X-Hub-Signature-256 (HMAC-SHA256 тела секретом App) проверяется на каждом запросе, неверная — 401. Тело — JSON до 2 МБ. Ответ 202 отдаётся сразу, доставка пишется в базу вместе с событием outbox, обработка — в воркере; повторная доставка с тем же X-GitHub-Delivery (7 дней) не обрабатывается. Маршрут есть, только если задан GITHUB_APP_ID.
// @Tags         github
// @Accept       json
// @Produce      json
// @Param        X-Hub-Signature-256  header  string  true  "sha256=<hex HMAC тела>"
// @Param        X-GitHub-Delivery    header  string  true  "id доставки"
// @Param        X-GitHub-Event       header  string  true  "тип события"
// @Param        body  body      object  true  "payload события GitHub"
// @Success      202   {object}  map[string]bool  "accepted: true"
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED — нет заголовка доставки или события; INVALID_JSON — тело не JSON"
// @Failure      401   {object}  apierr.Response  "неверная подпись"
// @Failure      413   {object}  apierr.Response  "PAYLOAD_TOO_LARGE"
// @Router       /api/v1/github/webhook [post]
func (h *Webhook) receive(c *gin.Context) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxPayloadBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpserver.Abort(c, apierr.PayloadTooLarge("Тело вебхука больше 2 МБ"))
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
	var missing []apierr.FieldError
	for h, v := range map[string]string{DeliveryHeader: delivery, EventHeader: event} {
		if v == "" {
			missing = append(missing, apierr.FieldError{Field: h, Reason: "обязательный заголовок"})
		}
	}
	if len(missing) > 0 {
		slices.SortFunc(missing, func(a, b apierr.FieldError) int { return strings.Compare(a.Field, b.Field) })
		httpserver.Abort(c, apierr.BadRequest(apierr.CodeValidation, "Нет заголовков доставки GitHub").With("fields", missing))
		return
	}
	if !json.Valid(body) {
		// Вебхук App настроен с типом application/x-www-form-urlencoded — нужен application/json.
		httpserver.Abort(c, apierr.BadRequest(apierr.CodeInvalidJSON, "Тело вебхука — не JSON: в настройках App нужен Content type application/json"))
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
