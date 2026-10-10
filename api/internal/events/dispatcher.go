package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/hibiken/asynq"
)

// TaskTypePrefix — префикс типа задания asynq для событий: "event:task.created".
const TaskTypePrefix = "event:"

// Envelope — то, что лежит в payload задания asynq.
type Envelope struct {
	EventID string          `json:"event_id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Handler обрабатывает одно событие. Ошибка — asynq повторит задание.
type Handler func(ctx context.Context, env Envelope) error

// Dispatcher раздаёт события обработчикам по типу. Домены регистрируют свои обработчики
// при старте воркера; у одного типа может быть несколько обработчиков.
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: map[string][]Handler{}}
}

// On регистрирует обработчик для типа события ("*" — для всех).
func (d *Dispatcher) On(eventType string, h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[eventType] = append(d.handlers[eventType], h)
}

// ProcessTask — обработчик asynq: разбирает конверт и вызывает обработчики события.
func (d *Dispatcher) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var env Envelope
	if err := json.Unmarshal(t.Payload(), &env); err != nil {
		// Битый конверт повторять бессмысленно.
		return fmt.Errorf("event envelope: %w: %w", err, asynq.SkipRetry)
	}
	if env.Type == "" {
		env.Type = strings.TrimPrefix(t.Type(), TaskTypePrefix)
	}

	d.mu.RLock()
	hs := append(append([]Handler{}, d.handlers[env.Type]...), d.handlers["*"]...)
	d.mu.RUnlock()

	var errs []error
	for _, h := range hs {
		if err := h(ctx, env); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("event %s: %w", env.Type, errors.Join(errs...))
	}
	slog.Debug("event handled", "type", env.Type, "event_id", env.EventID, "handlers", len(hs))
	return nil
}

// Mux возвращает asynq.ServeMux, в котором все задания "event:*" идут в диспетчер.
func (d *Dispatcher) Mux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.Handle(TaskTypePrefix, d) // префиксное совпадение: event:task.created, event:sprint.closed …
	return mux
}
