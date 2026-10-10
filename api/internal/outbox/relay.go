// Package outbox переносит события из таблицы outbox_events в очередь asynq (ТЗ §9.4).
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/queue"
)

const (
	batchSize       = 100
	pollInterval    = time.Second
	maxBackoff      = 30 * time.Second
	batchTimeout    = 30 * time.Second
	cleanupInterval = time.Hour
	retention       = "7 days" // ТЗ §3.8; считается на стороне базы
	taskMaxRetry    = 10
	taskRetention   = 24 * time.Hour
)

// Relay читает неотправленные события по порядку, кладёт их в asynq и помечает отправленными.
type Relay struct {
	db     *gorm.DB
	client *asynq.Client
}

func NewRelay(db *gorm.DB, client *asynq.Client) *Relay {
	return &Relay{db: db, client: client}
}

// Run чистит отправленные при старте и потом раз в час, опрашивает outbox раз в секунду
// и возвращается, когда ctx отменён. Текущая пачка дорабатывается на своём контексте,
// чтобы остановка воркера не откатывала уже поставленные в очередь события.
// После ошибки интервал опроса растёт до 30 с: при лежащем Redis не держим транзакции каждую секунду.
func (r *Relay) Run(ctx context.Context) {
	r.cleanupLogged()

	cleanup := time.NewTicker(cleanupInterval)
	defer cleanup.Stop()

	failures := 0
	for {
		wait := pollInterval
		if failures > 0 {
			wait = min(pollInterval<<uint(failures), maxBackoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-cleanup.C:
			r.cleanupLogged()
		case <-time.After(wait):
			if err := r.drain(); err != nil {
				failures++
				slog.Error("outbox relay", "err", err, "retry_in", min(pollInterval<<uint(failures), maxBackoff))
				continue
			}
			failures = 0
		}
	}
}

// drain переносит всё, что накопилось: пачка за пачкой, пока очередная не окажется неполной.
func (r *Relay) drain() error {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), batchTimeout)
		n, err := r.RunOnce(ctx)
		cancel()
		if err != nil {
			return err
		}
		if n < batchSize {
			return nil
		}
	}
}

func (r *Relay) cleanupLogged() {
	ctx, cancel := context.WithTimeout(context.Background(), batchTimeout)
	defer cancel()
	n, err := r.Cleanup(ctx)
	switch {
	case err != nil:
		slog.Error("outbox cleanup", "err", err)
	case n > 0:
		slog.Info("outbox cleanup", "deleted", n)
	}
}

// RunOnce переносит одну пачку событий. Возвращает число отправленных.
// Пачка берётся под FOR UPDATE SKIP LOCKED: несколько воркеров не отправят одно событие дважды.
// Любая ошибка откатывает транзакцию целиком — события остаются неотправленными и уйдут позже
// в том же порядке.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	sent := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var batch []events.OutboxEvent
		if err := tx.Raw(`SELECT id, type, payload, created_at, sent_at FROM outbox_events
			WHERE sent_at IS NULL ORDER BY created_at, id LIMIT ? FOR UPDATE SKIP LOCKED`, batchSize).
			Scan(&batch).Error; err != nil {
			return fmt.Errorf("select: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(batch))
		for _, ev := range batch {
			if err := r.enqueue(ctx, ev); err != nil {
				return err
			}
			ids = append(ids, ev.ID)
		}
		if err := tx.Exec(`UPDATE outbox_events SET sent_at = now() WHERE id IN ?`, ids).Error; err != nil {
			return fmt.Errorf("mark sent: %w", err)
		}
		sent = len(ids)
		return nil
	})
	return sent, err
}

// enqueue кладёт событие в asynq. TaskID = id события: повторная постановка после сбоя
// между Enqueue и коммитом не создаёт дубликата, пока задание хранится в Redis (24 часа).
// Дольше дубликаты отсекает диспетчер по event_id для каждого обработчика (7 дней).
func (r *Relay) enqueue(ctx context.Context, ev events.OutboxEvent) error {
	env := events.Envelope{EventID: ev.ID.String(), Type: ev.Type, Payload: json.RawMessage(ev.Payload)}
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("envelope %s: %w", ev.ID, err)
	}
	task := asynq.NewTask(events.TaskTypePrefix+ev.Type, payload)
	_, err = r.client.EnqueueContext(ctx, task,
		asynq.TaskID(ev.ID.String()),
		asynq.Queue(queue.QueueDefault),
		asynq.MaxRetry(taskMaxRetry),
		asynq.Retention(taskRetention),
	)
	if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
		return fmt.Errorf("enqueue %s: %w", ev.Type, err)
	}
	return nil
}

// Cleanup удаляет отправленные события старше 7 дней. Возраст считает база — тот же источник
// времени, что ставил sent_at.
func (r *Relay) Cleanup(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`DELETE FROM outbox_events WHERE sent_at IS NOT NULL AND sent_at < now() - ?::interval`, retention)
	return res.RowsAffected, res.Error
}
