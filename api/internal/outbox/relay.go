// Package outbox переносит события из таблицы outbox_events в очередь asynq (ТЗ §9.4).
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/queue"
)

const (
	batchSize       = 100
	pollInterval    = time.Second
	retention       = 7 * 24 * time.Hour
	cleanupInterval = time.Hour
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

// Run опрашивает outbox раз в секунду и раз в час чистит отправленные старше 7 дней.
// Ошибка Redis не останавливает цикл: события остаются в таблице до восстановления.
func (r *Relay) Run(ctx context.Context) {
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	cleanup := time.NewTicker(cleanupInterval)
	defer cleanup.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			for {
				n, err := r.RunOnce(ctx)
				if err != nil {
					slog.Error("outbox relay", "err", err)
					break
				}
				if n < batchSize {
					break
				}
			}
		case <-cleanup.C:
			if n, err := r.Cleanup(ctx); err != nil {
				slog.Error("outbox cleanup", "err", err)
			} else if n > 0 {
				slog.Info("outbox cleanup", "deleted", n)
			}
		}
	}
}

// RunOnce переносит одну пачку событий. Возвращает число отправленных.
// Пачка берётся под FOR UPDATE SKIP LOCKED: несколько воркеров не отправят одно событие дважды.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	sent := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var batch []events.OutboxEvent
		if err := tx.Raw(`SELECT id, type, payload, created_at, sent_at FROM outbox_events
			WHERE sent_at IS NULL ORDER BY created_at, id LIMIT ? FOR UPDATE SKIP LOCKED`, batchSize).
			Scan(&batch).Error; err != nil {
			return fmt.Errorf("select: %w", err)
		}
		for _, ev := range batch {
			if err := r.enqueue(ctx, ev); err != nil {
				// Остальные события ждут следующего прохода; порядок сохраняется.
				return err
			}
			if err := tx.Exec(`UPDATE outbox_events SET sent_at = now() WHERE id = ?`, ev.ID).Error; err != nil {
				return fmt.Errorf("mark sent: %w", err)
			}
			sent++
		}
		return nil
	})
	return sent, err
}

// enqueue кладёт событие в asynq. TaskID = id события: повторная постановка после сбоя
// между Enqueue и коммитом не создаёт дубликата.
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

// Cleanup удаляет отправленные события старше 7 дней.
func (r *Relay) Cleanup(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`DELETE FROM outbox_events WHERE sent_at IS NOT NULL AND sent_at < ?`, time.Now().Add(-retention))
	return res.RowsAffected, res.Error
}
