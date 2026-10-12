package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

// Processor обрабатывает события вебхука в воркере. Связь задач с ветками и PR и смена статуса
// (§12.3, §12.4) — следующий шаг; пока событие только разбирается и пишется в лог.
type Processor struct{}

func NewProcessor() *Processor { return &Processor{} }

// Mount подключает обработчик задания github:event.
func Mount(mux *asynq.ServeMux, p *Processor) {
	mux.HandleFunc(TaskEvent, p.handle)
}

func (p *Processor) handle(ctx context.Context, t *asynq.Task) error {
	var job eventJob
	if err := json.Unmarshal(t.Payload(), &job); err != nil {
		return fmt.Errorf("github: event job: %w: %w", err, asynq.SkipRetry)
	}
	slog.InfoContext(ctx, "github: event", "event", job.Event, "delivery", job.Delivery)
	return nil
}
