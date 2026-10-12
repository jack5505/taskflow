package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Processor обрабатывает доставки вебхука в воркере. Связь задач с ветками и PR и смена статуса
// (§12.3, §12.4) — следующий шаг; пока доставка только разбирается и пишется в лог.
type Processor struct{}

func NewProcessor() *Processor { return &Processor{} }

// Register подключает обработчик события github.delivery. Подключается всегда, а не только при
// заданном GITHUB_APP_ID: событие могло прийти от API с другими настройками и иначе потерялось бы.
func Register(d *events.Dispatcher, p *Processor) {
	d.On(events.GitHubDelivery, "github", p.handle)
}

func (p *Processor) handle(ctx context.Context, env events.Envelope) error {
	var d Delivery
	if err := json.Unmarshal(env.Payload, &d); err != nil {
		return fmt.Errorf("github: %s payload: %w", env.Type, err)
	}
	slog.InfoContext(ctx, "github: delivery", "event", d.Event, "delivery", d.Delivery)
	return nil
}
