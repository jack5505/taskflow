package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/jack5505/taskflow/api/internal/queue"
)

// processedTTL — сколько помним обработанный update_id (ProcessedUpdate, §3.8, §8.8).
const processedTTL = 24 * time.Hour

// updateTimeout — предел обработки одного обновления в воркере.
const updateTimeout = time.Minute

// updateJob — payload задания telegram:update: обновление как пришло и время приёма
// (от него считается срок answerCallbackQuery).
type updateJob struct {
	ReceivedAt time.Time       `json:"received_at"`
	Update     json.RawMessage `json:"update"`
}

// Intake принимает обновления от Telegram (webhook или long polling) и ставит их в очередь.
//
// ProcessedUpdate хранится в Redis, а не в таблице: ключ с TTL 24 часа удаляется сам, без
// уборки, а SET NX атомарно решает гонку двух одновременных доставок одного update_id. Redis
// здесь не новая точка отказа: без него не поставить и задание asynq, и Telegram повторит
// доставку (webhook ответит 500).
type Intake struct {
	rdb  *redis.Client
	jobs *asynq.Client
	now  func() time.Time
}

func NewIntake(rdb *redis.Client, jobs *asynq.Client) *Intake {
	return &Intake{rdb: rdb, jobs: jobs, now: time.Now}
}

func processedKey(updateID int64) string { return "tg:update:" + strconv.FormatInt(updateID, 10) }

// Accept запоминает update_id и ставит задание обработки. false — этот update_id уже принят
// за последние 24 часа, повторного действия не будет (§8.8). Ошибка — обновление не принято,
// Telegram должен доставить его снова.
//
// Задание без повторов asynq: update_id обрабатывается не более одного раза, а повтор после
// частично выполненного действия (время списано, ответ не ушёл) дал бы двойное списание.
func (in *Intake) Accept(ctx context.Context, updateID int64, raw []byte) (bool, error) {
	now := in.now()
	key := processedKey(updateID)
	fresh, err := in.rdb.SetNX(ctx, key, now.UTC().Format(time.RFC3339), processedTTL).Result()
	if err != nil {
		return false, fmt.Errorf("telegram: processed update: %w", err)
	}
	if !fresh {
		return false, nil
	}
	payload, err := json.Marshal(updateJob{ReceivedAt: now, Update: raw})
	if err == nil {
		_, err = in.jobs.EnqueueContext(ctx, asynq.NewTask(TaskUpdate, payload),
			asynq.Queue(queue.QueueDefault), asynq.MaxRetry(0), asynq.Timeout(updateTimeout))
	}
	if err != nil {
		// Задание не поставлено — забываем update_id, чтобы повторная доставка прошла.
		_ = in.rdb.Del(context.WithoutCancel(ctx), key).Err()
		return false, fmt.Errorf("telegram: enqueue update %d: %w", updateID, err)
	}
	return true, nil
}
