package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/jack5505/taskflow/api/internal/queue"
)

// Операции с сообщением.
const (
	OpSend   = "send"
	OpEdit   = "edit"
	OpDelete = "delete"
)

// Лимиты отправки (§8.8).
const (
	MaxAttempts = 5                // после стольких неудачных попыток сообщение отбрасывается
	GroupLimit  = 20               // сообщений в минуту в одну группу
	limitWindow = time.Minute      // окно лимита
	sendTimeout = 30 * time.Second // предел одного вызова Bot API
)

// infraRetries — повторы asynq при сбое воркера (не Telegram).
const infraRetries = 3

// TempReplyTTL — через сколько бот удаляет свой временный ответ в группе (§8.1, §8.4, §8.5).
const TempReplyTTL = 30 * time.Second

// Message — что сделать с сообщением: отправить, изменить или удалить. Это payload задания
// telegram:send, поэтому только сериализуемые поля.
type Message struct {
	Op        string `json:"op"`
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id,omitempty"` // edit, delete
	ReplyTo   int    `json:"reply_to,omitempty"`   // send: ответом на это сообщение
	Text      string `json:"text,omitempty"`
	HTML      bool   `json:"html,omitempty"`
	// Keyboard — кнопки под сообщением (send, edit).
	Keyboard *models.InlineKeyboardMarkup `json:"keyboard,omitempty"`
	// DeleteAfter — удалить отправленное сообщение через столько секунд (временный ответ в группе).
	DeleteAfter int `json:"delete_after,omitempty"`
	// Attempt — сколько попыток уже не удалось.
	Attempt int `json:"attempt,omitempty"`
	// Kind и Ref — сообщение, которое собирается при отправке по текущим данным (Router.Kind):
	// например карточка задачи (Kind "card", Ref — id задачи). Text, ChatID и MessageID тогда
	// заполняет Render.
	Kind string `json:"kind,omitempty"`
	Ref  string `json:"ref,omitempty"`
	// Replace — send вместо edit, который Telegram отклонил (сообщение старше 48 часов или удалено).
	Replace bool `json:"replace,omitempty"`
	// stamp — версия данных, по которой Render собрал сообщение (для KindHooks.Sent).
	stamp int
}

// KindHooks — сборка сообщения вида Kind при отправке и действие после неё.
type KindHooks struct {
	// Render заполняет сообщение по текущим данным; false — отправлять нечего (группа отключена,
	// сообщения нет). Для каждой попытки вызывается заново.
	Render func(ctx context.Context, m *Message) (bool, error)
	// Sent вызывается после успешной операции; sent — новое сообщение (для send), иначе nil.
	Sent func(ctx context.Context, m Message, sent *models.Message) error
	// ReplaceOnEditError — edit, отклонённый Telegram (400), повторить как send с Replace.
	ReplaceOnEditError bool
}

// Queue ставит сообщения в очередь отправки. Отправляет их только воркер: так лимиты
// и повторы в одном месте, а HTTP-обработчики и обработчики бота не ждут Telegram.
type Queue struct{ jobs *asynq.Client }

func NewQueue(jobs *asynq.Client) *Queue { return &Queue{jobs: jobs} }

// Send ставит сообщение в очередь; delay > 0 — отправить не раньше чем через delay.
func (q *Queue) Send(ctx context.Context, m Message, delay time.Duration) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("telegram: message payload: %w", err)
	}
	// Повторы после ответа Telegram ведёт Deliverer (счётчик Attempt). Повторы asynq — только
	// на случай сбоя самого воркера: Redis лимитера недоступен, повтор не поставился.
	opts := []asynq.Option{asynq.Queue(queue.QueueDefault), asynq.MaxRetry(infraRetries), asynq.Timeout(sendTimeout + 5*time.Second)}
	if delay > 0 {
		opts = append(opts, asynq.ProcessIn(delay))
	}
	if _, err := q.jobs.EnqueueContext(ctx, asynq.NewTask(TaskSend, payload), opts...); err != nil {
		return fmt.Errorf("telegram: enqueue message: %w", err)
	}
	return nil
}

// SendOnce — Send, который не ставит второе такое же задание id, пока первое ждёт или
// выполняется: так события за одну секунду по одной задаче дают одну отправку.
func (q *Queue) SendOnce(ctx context.Context, m Message, delay time.Duration, id string) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("telegram: message payload: %w", err)
	}
	_, err = q.jobs.EnqueueContext(ctx, asynq.NewTask(TaskSend, payload), asynq.Queue(queue.QueueDefault),
		asynq.MaxRetry(infraRetries), asynq.Timeout(sendTimeout+5*time.Second), asynq.ProcessIn(delay), asynq.TaskID(id))
	if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
		return fmt.Errorf("telegram: enqueue message: %w", err)
	}
	return nil
}

// Deliverer — обработчик заданий telegram:send в воркере. Повторы он ведёт сам, а не через
// повторы asynq: задержка зависит от ответа Telegram (retry_after), а ожидание лимита группы
// не считается неудачной попыткой.
type Deliverer struct {
	api   API
	limit *Limiter
	out   *Queue
	kinds map[string]KindHooks
}

func NewDeliverer(api API, rdb *redis.Client, out *Queue) *Deliverer {
	return &Deliverer{api: api, limit: NewLimiter(rdb), out: out}
}

// WithRouter — сборка сообщений по видам, зарегистрированным в роутере (Router.Kind).
func (d *Deliverer) WithRouter(rt *Router) *Deliverer {
	d.kinds = rt.kinds
	return d
}

func (d *Deliverer) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var m Message
	if err := json.Unmarshal(t.Payload(), &m); err != nil {
		return fmt.Errorf("telegram: message payload: %w: %w", err, asynq.SkipRetry)
	}
	hooks, kind := d.kinds[m.Kind]
	if m.Kind != "" {
		if !kind || hooks.Render == nil {
			slog.Error("telegram: unknown message kind, dropped", "kind", m.Kind)
			return nil
		}
		ok, err := hooks.Render(ctx, &m)
		if err != nil || !ok {
			return err // база недоступна — asynq повторит задание (infraRetries)
		}
	}
	if m.ChatID < 0 && m.Op != OpDelete {
		// Группа: не больше GroupLimit сообщений в минуту. Удаление сообщением не считается.
		wait, err := d.limit.Reserve(ctx, m.ChatID)
		if err != nil {
			return err // Redis недоступен — asynq повторит задание (infraRetries)
		}
		if wait > 0 {
			return d.out.Send(ctx, m, wait)
		}
	}
	sent, err := d.call(ctx, m)
	if err == nil && hooks.Sent != nil {
		if err := hooks.Sent(ctx, m, sent); err != nil {
			// Сообщение уже в Telegram — повторять нельзя, иначе будет второе.
			slog.Error("telegram: after send", "kind", m.Kind, "ref", m.Ref, "err", err)
		}
	}
	if err != nil && hooks.ReplaceOnEditError && m.Op == OpEdit && errors.Is(err, bot.ErrorBadRequest) {
		// Сообщение нельзя изменить (старше 48 часов или удалено) — публикуем новое (§8.3).
		slog.Info("telegram: edit refused, sending new message", "kind", m.Kind, "ref", m.Ref, "err", err)
		m.Op, m.MessageID, m.Replace = OpSend, 0, true
		return d.out.Send(ctx, m, 0)
	}
	if err == nil {
		if m.DeleteAfter > 0 && sent != nil {
			del := Message{Op: OpDelete, ChatID: m.ChatID, MessageID: sent.ID}
			return d.out.Send(ctx, del, time.Duration(m.DeleteAfter)*time.Second)
		}
		return nil
	}

	var migrated *bot.MigrateError
	if errors.As(err, &migrated) && migrated.MigrateToChatID != 0 && m.Op == OpSend {
		// Группа стала супергруппой (§8.2, п. 6): то же сообщение — в новый чат. Проекты группы
		// переписывает обработчик служебного сообщения migrate_to_chat_id.
		slog.Info("telegram: chat migrated, resending", "chat_id", m.ChatID, "to", migrated.MigrateToChatID)
		// Номера сообщений старой группы в новой не действуют — без ответа на сообщение.
		m.ChatID, m.ReplyTo = int64(migrated.MigrateToChatID), 0
		return d.out.Send(ctx, m, 0)
	}
	m.Attempt++
	log := slog.With("op", m.Op, "chat_id", m.ChatID, "message_id", m.MessageID, "attempt", m.Attempt, "err", err)
	if permanent(err) {
		log.Error("telegram: message dropped, Telegram refused it")
		return nil
	}
	if m.Attempt >= MaxAttempts {
		log.Error("telegram: message dropped after retries")
		return nil
	}
	delay := backoff(m.Attempt)
	var tooMany *bot.TooManyRequestsError
	if errors.As(err, &tooMany) && tooMany.RetryAfter > 0 {
		delay = time.Duration(tooMany.RetryAfter) * time.Second
	}
	log.Warn("telegram: message retry", "in", delay)
	return d.out.Send(ctx, m, delay)
}

// call — один вызов Bot API. Для edit и delete сообщения в ответе нет.
func (d *Deliverer) call(ctx context.Context, m Message) (*models.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	parseMode := models.ParseMode("")
	if m.HTML {
		parseMode = models.ParseModeHTML
	}
	switch m.Op {
	case OpSend:
		p := &bot.SendMessageParams{ChatID: m.ChatID, Text: m.Text, ParseMode: parseMode,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()}}
		if m.Keyboard != nil {
			p.ReplyMarkup = m.Keyboard
		}
		if m.ReplyTo != 0 {
			// Исходное сообщение могли удалить — отправляем и без него.
			p.ReplyParameters = &models.ReplyParameters{MessageID: m.ReplyTo, AllowSendingWithoutReply: true}
		}
		return d.api.SendMessage(ctx, p)
	case OpEdit:
		p := &bot.EditMessageTextParams{ChatID: m.ChatID, MessageID: m.MessageID, Text: m.Text, ParseMode: parseMode,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()}}
		if m.Keyboard != nil {
			p.ReplyMarkup = m.Keyboard
		}
		_, err := d.api.EditMessageText(ctx, p)
		if err != nil && strings.Contains(err.Error(), "message is not modified") {
			return nil, nil // то же содержимое — цель достигнута
		}
		return nil, err
	case OpDelete:
		_, err := d.api.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: m.ChatID, MessageID: m.MessageID})
		return nil, err
	default:
		return nil, fmt.Errorf("%w: неизвестная операция %q", errPermanent, m.Op)
	}
}

var errPermanent = errors.New("telegram: сообщение нельзя отправить")

// permanent — ошибка, которую повтор не исправит: бота удалили из чата, сообщения нет,
// неверный запрос или токен. 429, 5xx и сетевые ошибки повторяются.
func permanent(err error) bool {
	for _, e := range []error{errPermanent, bot.ErrorBadRequest, bot.ErrorForbidden, bot.ErrorUnauthorized, bot.ErrorNotFound} {
		if errors.Is(err, e) {
			return true
		}
	}
	return bot.IsMigrateError(err)
}

// backoff — пауза перед повтором без retry_after: 1, 2, 4, 8 секунд.
func backoff(attempt int) time.Duration {
	return time.Duration(1<<(attempt-1)) * time.Second
}
