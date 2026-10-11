package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/jack5505/taskflow/api/internal/audit"
)

// AckAfter — если обработчик кнопки не уложился в это время от приёма обновления, нажатие
// подтверждается пустым answerCallbackQuery, а ответ уходит сообщением: Telegram ждёт
// подтверждения не дольше 3 секунд (§8.8), секунда — запас на очередь и сеть.
const AckAfter = 2 * time.Second

// ackTimeout — предел самого вызова answerCallbackQuery.
const ackTimeout = 5 * time.Second

// Processor — обработчик заданий telegram:update в воркере.
type Processor struct {
	api      API
	router   *Router
	out      *Queue
	now      func() time.Time
	ackAfter time.Duration
}

func NewProcessor(api API, router *Router, out *Queue) *Processor {
	return &Processor{api: api, router: router, out: out, now: time.Now, ackAfter: AckAfter}
}

func (p *Processor) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var job updateJob
	if err := json.Unmarshal(t.Payload(), &job); err != nil {
		return fmt.Errorf("telegram: update job: %w: %w", err, asynq.SkipRetry)
	}
	var upd models.Update
	if err := json.Unmarshal(job.Update, &upd); err != nil {
		return fmt.Errorf("telegram: update: %w: %w", err, asynq.SkipRetry)
	}
	p.Handle(ctx, &upd, job.ReceivedAt)
	return nil
}

// Handle обрабатывает обновление: находит обработчик, вызывает его от имени бота
// (источник действия — telegram, §3.5–3.7) и подтверждает нажатие кнопки.
func (p *Processor) Handle(ctx context.Context, upd *models.Update, receivedAt time.Time) {
	r, h := p.router.route(upd, p.out)
	if h == nil {
		return
	}
	ctx = audit.WithSource(ctx, audit.SourceBot)
	log := slog.With("update_id", upd.ID, "command", r.Command, "action", r.Action, "chat_id", r.Chat.ID)

	if !r.IsCallback() {
		if err := h(ctx, r); err != nil {
			log.Error("telegram: handler failed", "err", err)
			if !r.service {
				p.sendLater(ctx, r.reply(Text("internal_error", nil)))
			}
		}
		return
	}

	ack := &acker{api: p.api, id: r.callbackID}
	// Обработчик медленный (база, блокировка задачи) — подтверждаем пустым ответом заранее.
	timer := time.AfterFunc(max(receivedAt.Add(p.ackAfter).Sub(p.now()), 0), func() { ack.send(ctx, "", false) })
	err := h(ctx, r)
	timer.Stop()
	text, alert := r.takeAnswer()
	if err != nil {
		log.Error("telegram: handler failed", "err", err)
		text, alert = Text("internal_error", nil), true
	}
	if !ack.send(ctx, text, alert) && text != "" {
		// Подтверждение уже ушло пустым или не прошло — ответ показываем временным сообщением в чате.
		m := r.reply(text)
		if !r.Private() {
			m.DeleteAfter = int(TempReplyTTL.Seconds())
		}
		p.sendLater(ctx, m)
	}
}

func (p *Processor) sendLater(ctx context.Context, m Message) {
	if m.ChatID == 0 {
		return
	}
	if err := p.out.Send(ctx, m, 0); err != nil {
		slog.Error("telegram: reply not queued", "chat_id", m.ChatID, "err", err)
	}
}

// acker подтверждает нажатие кнопки ровно один раз: таймером или после обработчика.
type acker struct {
	api  API
	id   string
	mu   sync.Mutex
	done bool
}

// send вызывает answerCallbackQuery. true — этот вызов показал ответ; false — подтверждение уже
// было или Telegram его не принял (текст тогда нужно показать сообщением).
func (a *acker) send(ctx context.Context, text string, alert bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.done {
		return false
	}
	a.done = true
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ackTimeout)
	defer cancel()
	if _, err := a.api.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: a.id, Text: text, ShowAlert: alert,
	}); err != nil {
		// Подтверждение не повторяем: через 3 секунды Telegram его уже не примет.
		slog.Warn("telegram: answerCallbackQuery", "err", err)
		return false
	}
	return true
}

// Mount подключает обработчики заданий бота к серверу asynq воркера.
func Mount(mux *asynq.ServeMux, api API, router *Router, rdb *redis.Client, jobs *asynq.Client) {
	out := NewQueue(jobs)
	mux.Handle(TaskUpdate, NewProcessor(api, router, out))
	mux.Handle(TaskSend, NewDeliverer(api, rdb, out).WithRouter(router))
}
