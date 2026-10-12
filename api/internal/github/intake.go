package github

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
)

// deliveryTTL — сколько помним принятую доставку X-GitHub-Delivery (§12.8: 7 дней).
const deliveryTTL = 7 * 24 * time.Hour

// Delivery — payload события github.delivery: доставка как пришла.
type Delivery struct {
	Event    string          `json:"event"`
	Delivery string          `json:"delivery"`
	Payload  json.RawMessage `json:"payload"`
}

// Intake принимает доставки вебхуков: строка github_deliveries и событие outbox пишутся одной
// транзакцией (§12.2, как остальные события §9.4). Повтор того же id отсекает первичный ключ,
// а потерять принятую доставку нельзя — событие уже в outbox.
type Intake struct {
	db  *gorm.DB
	now func() time.Time
}

func NewIntake(db *gorm.DB) *Intake { return &Intake{db: db, now: time.Now} }

// Accept запоминает доставку и ставит её обработку. false — эта доставка уже принята.
// Ошибка — доставка не принята, GitHub повторит её.
func (in *Intake) Accept(ctx context.Context, event, delivery string, payload json.RawMessage) (bool, error) {
	var fresh bool
	err := in.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if fresh, err = insertDelivery(tx, delivery, event); err != nil || !fresh {
			return err
		}
		return events.Publish(tx, events.GitHubDelivery, Delivery{Event: event, Delivery: delivery, Payload: payload})
	})
	return fresh, err
}

// CleanupDeliveries удаляет доставки старше 7 дней (воркер, раз в сутки).
func (in *Intake) CleanupDeliveries(ctx context.Context) (int64, error) {
	return deleteDeliveriesBefore(in.db.WithContext(ctx), in.now().Add(-deliveryTTL))
}
