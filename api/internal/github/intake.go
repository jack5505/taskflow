package github

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/jack5505/taskflow/api/internal/queue"
)

// deliveryTTL — сколько помним принятую доставку X-GitHub-Delivery (§12.8: 7 дней). GitHub
// повторяет доставку не позже чем через несколько дней.
const deliveryTTL = 7 * 24 * time.Hour

// eventTimeout — предел обработки одного события в воркере.
const eventTimeout = time.Minute

// eventJob — payload задания github:event: событие как пришло.
type eventJob struct {
	Event      string          `json:"event"`
	Delivery   string          `json:"delivery"`
	ReceivedAt time.Time       `json:"received_at"`
	Payload    json.RawMessage `json:"payload"`
}

// Intake принимает доставки вебхуков и ставит их в очередь. Принятые доставки хранятся в
// Redis с TTL: SET NX атомарно решает гонку двух одновременных доставок одного id, а ключ
// удаляется сам, без уборки (как ProcessedUpdate бота, §8.8).
type Intake struct {
	rdb  *redis.Client
	jobs *asynq.Client
	now  func() time.Time
}

func NewIntake(rdb *redis.Client, jobs *asynq.Client) *Intake {
	return &Intake{rdb: rdb, jobs: jobs, now: time.Now}
}

func deliveryKey(id string) string { return "gh:delivery:" + id }

// Accept запоминает доставку и ставит задание обработки. false — эта доставка уже принята,
// повторной обработки не будет (§12.2). Ошибка — доставка не принята, GitHub повторит её.
func (in *Intake) Accept(ctx context.Context, event, delivery string, payload []byte) (bool, error) {
	now := in.now()
	key := deliveryKey(delivery)
	fresh, err := in.rdb.SetNX(ctx, key, now.UTC().Format(time.RFC3339), deliveryTTL).Result()
	if err != nil {
		return false, fmt.Errorf("github: delivery: %w", err)
	}
	if !fresh {
		return false, nil
	}
	raw, err := json.Marshal(eventJob{Event: event, Delivery: delivery, ReceivedAt: now, Payload: payload})
	if err == nil {
		// Обработка повторяема: состояние PR и связи пишутся как есть, переходы проверяют статус.
		_, err = in.jobs.EnqueueContext(ctx, asynq.NewTask(TaskEvent, raw),
			asynq.Queue(queue.QueueDefault), asynq.MaxRetry(5), asynq.Timeout(eventTimeout))
	}
	if err != nil {
		// Задание не поставлено — забываем доставку, чтобы повтор GitHub прошёл.
		_ = in.rdb.Del(context.WithoutCancel(ctx), key).Err()
		return false, fmt.Errorf("github: enqueue %s: %w", delivery, err)
	}
	return true, nil
}
