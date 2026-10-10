package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// TaskTypePrefix — префикс типа задания asynq для событий: "event:task.created".
const TaskTypePrefix = "event:"

// doneTTL — сколько помнить, какие обработчики уже отработали событие.
// Совпадает со сроком хранения outbox (ТЗ §3.8): дольше событие повторно прийти не может.
const doneTTL = 7 * 24 * time.Hour

// Envelope — то, что лежит в payload задания asynq.
type Envelope struct {
	EventID string          `json:"event_id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Handler обрабатывает одно событие. Ошибка — asynq повторит задание,
// но обработчики, которые уже отработали это событие, второй раз не вызываются.
type Handler func(ctx context.Context, env Envelope) error

type namedHandler struct {
	name string
	fn   Handler
}

// Dispatcher раздаёт события обработчикам по типу. Домены регистрируют свои обработчики
// при старте воркера; у одного типа может быть несколько обработчиков.
type Dispatcher struct {
	rdb      *redis.Client // nil — без учёта выполненных обработчиков (тесты)
	mu       sync.RWMutex
	handlers map[string][]namedHandler
}

// NewDispatcher создаёт диспетчер. rdb нужен, чтобы при повторе задания не вызывать
// обработчики, которые уже успели отработать (например, сообщение в Telegram ушло, а письмо — нет).
func NewDispatcher(rdb *redis.Client) *Dispatcher {
	return &Dispatcher{rdb: rdb, handlers: map[string][]namedHandler{}}
}

// On регистрирует обработчик name для типа события ("*" — для всех).
// name уникален в пределах типа: по нему запоминается факт выполнения.
func (d *Dispatcher) On(eventType, name string, h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, existing := range d.handlers[eventType] {
		if existing.name == name {
			panic(fmt.Sprintf("events: обработчик %q для %q зарегистрирован дважды", name, eventType))
		}
	}
	d.handlers[eventType] = append(d.handlers[eventType], namedHandler{name: name, fn: h})
}

// ProcessTask — обработчик asynq: разбирает конверт и вызывает обработчики события.
func (d *Dispatcher) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var env Envelope
	if err := json.Unmarshal(t.Payload(), &env); err != nil {
		// Битый конверт повторять бессмысленно.
		return fmt.Errorf("event envelope: %w: %w", err, asynq.SkipRetry)
	}
	if env.Type == "" || env.EventID == "" {
		return fmt.Errorf("event envelope без type или event_id: %w", asynq.SkipRetry)
	}

	d.mu.RLock()
	specific := append([]namedHandler{}, d.handlers[env.Type]...)
	hs := append(specific, d.handlers["*"]...)
	d.mu.RUnlock()

	if len(specific) == 0 {
		// Событие известно (Publish проверяет тип), но обработчика у него ещё нет —
		// это должно быть видно в логах, а не теряться молча.
		slog.Warn("event without handler", "type", env.Type, "event_id", env.EventID)
	}

	var errs []error
	for _, h := range hs {
		done, err := d.isDone(ctx, env.EventID, h.name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if done {
			continue
		}
		if err := h.fn(ctx, env); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h.name, err))
			continue
		}
		if err := d.markDone(ctx, env.EventID, h.name); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("event %s: %w", env.Type, errors.Join(errs...))
	}
	return nil
}

func doneKey(eventID string) string { return "event:done:" + eventID }

func (d *Dispatcher) isDone(ctx context.Context, eventID, name string) (bool, error) {
	if d.rdb == nil {
		return false, nil
	}
	ok, err := d.rdb.SIsMember(ctx, doneKey(eventID), name).Result()
	if err != nil {
		return false, fmt.Errorf("event done check: %w", err)
	}
	return ok, nil
}

func (d *Dispatcher) markDone(ctx context.Context, eventID, name string) error {
	if d.rdb == nil {
		return nil
	}
	pipe := d.rdb.TxPipeline()
	pipe.SAdd(ctx, doneKey(eventID), name)
	pipe.Expire(ctx, doneKey(eventID), doneTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("event done mark: %w", err)
	}
	return nil
}

// Mux возвращает asynq.ServeMux, в котором все задания "event:*" идут в диспетчер.
func (d *Dispatcher) Mux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.Handle(TaskTypePrefix, d) // префиксное совпадение: event:task.created, event:sprint.closed …
	return mux
}
