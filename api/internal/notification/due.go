package notification

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Виды напоминаний о сроке (§8.7).
const (
	DueTomorrow = "due_tomorrow" // срок завтра
	DueOverdue  = "overdue"      // срок просрочен
)

// DueHour — §8.7: «срок завтра» отправляется в 09:00 по часовому поясу пользователя. Если
// напоминание стало нужно позже (срок поставили днём, воркер стоял), оно уходит сразу, но не
// позже DueQuietFrom: ночью не будим, а «просрочено» отправится утром того же дня просрочки.
const (
	DueHour      = "09:00"
	DueQuietFrom = "22:00"
)

// DueEvery — как часто воркер ищет напоминания: пояса пользователей разные (бывают со сдвигом
// на 30 и 45 минут), поэтому не таймер на 09:00 сервера, а проверка раз в минуту.
const DueEvery = time.Minute

// Due — отправленное напоминание о сроке исполнителю задачи.
type Due struct {
	UserID     uuid.UUID
	TaskID     uuid.UUID
	Kind       string
	Key        string
	Title      string
	ProjectID  uuid.UUID
	ProjectKey string
	DueDate    time.Time
}

// dueCandidates — напоминания, которые пора отправить, и запись о них (идемпотентность: строка
// due_reminders на задачу, вид, срок и получателя). Получатель — активный исполнитель открытой
// задачи, участник проекта; проект не удалён и не в архиве. С 09:00 до 22:00 по поясу пользователя:
//   - «срок завтра» — срок равен завтрашней дате пользователя;
//   - «просрочено» — в день просрочки: срок равен вчерашней дате по поясу проекта (задача
//     просрочена по поясу проекта, §3.3), один раз.
//
// Если воркер стоял в 09:00, напоминание уходит, как только он поднимется в тот же день.
const dueCandidates = `WITH open AS (
	SELECT t.id AS task_id, t.assignee_id AS user_id, t.key, t.title, t.project_id, p.key AS project_key, t.due_date,
		(CAST(@now AS timestamptz) AT TIME ZONE u.timezone)::date AS user_today,
		(CAST(@now AS timestamptz) AT TIME ZONE p.timezone)::date AS project_today
	FROM tasks t
	JOIN projects p ON p.id = t.project_id AND p.deleted_at IS NULL AND NOT p.is_archived
	JOIN users u ON u.id = t.assignee_id AND u.is_active
	WHERE t.due_date IS NOT NULL AND t.deleted_at IS NULL AND t.status NOT IN ('done', 'cancelled')
		-- Грубая граница по дате UTC: пояса расходятся меньше чем на двое суток, а индекс
		-- tasks_open_due_idx отсекает остальные задачи.
		AND t.due_date BETWEEN CAST(@now AS timestamptz)::date - 2 AND CAST(@now AS timestamptz)::date + 2
		AND (CAST(@now AS timestamptz) AT TIME ZONE u.timezone)::time >= CAST(@hour AS time)
		AND (CAST(@now AS timestamptz) AT TIME ZONE u.timezone)::time < CAST(@quiet AS time)
		AND EXISTS (SELECT 1 FROM project_members m WHERE m.project_id = t.project_id AND m.user_id = u.id)
), cand AS (
	SELECT o.*, 'due_tomorrow' AS kind FROM open o WHERE o.due_date = o.user_today + 1
	UNION ALL
	SELECT o.*, 'overdue' AS kind FROM open o WHERE o.due_date = o.project_today - 1
), ins AS (
	INSERT INTO due_reminders (task_id, kind, due_date, user_id)
	SELECT task_id, kind, due_date, user_id FROM cand
	ON CONFLICT DO NOTHING
	RETURNING task_id, kind, due_date, user_id
)
SELECT cand.task_id, cand.user_id, cand.key, cand.title, cand.project_id, cand.project_key, cand.due_date, cand.kind
FROM cand JOIN ins USING (task_id, kind, due_date, user_id) ORDER BY cand.key, cand.kind`

// DueReminders — проход планировщика напоминаний (§8.7, §7.7): находит задачи со сроком завтра
// и просроченные сегодня, создаёт уведомления колокольчика типа due и после фиксации
// транзакции передаёт каждое напоминание в send (личное сообщение бота; nil — бот выключен).
// Ошибка send только пишется в лог: напоминание уже записано и в колокольчике есть.
func DueReminders(ctx context.Context, db *gorm.DB, now time.Time, send func(context.Context, Due) error) (int64, error) {
	var rows []Due
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(dueCandidates, map[string]any{"now": now, "hour": DueHour, "quiet": DueQuietFrom}).Scan(&rows).Error; err != nil {
			return fmt.Errorf("notification: due reminders: %w", err)
		}
		for _, r := range rows {
			taskID := r.TaskID
			if err := Create(ctx, tx, New{UserID: r.UserID, TaskID: &taskID, Type: TypeDue, Payload: map[string]any{
				"kind": r.Kind, "key": r.Key, "title": r.Title, "due_date": r.DueDate.Format(time.DateOnly),
				"project_id": r.ProjectID, "project_key": r.ProjectKey,
			}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if send != nil {
		for _, r := range rows {
			if err := send(ctx, r); err != nil {
				slog.Error("due reminder: telegram", "task_id", r.TaskID, "user_id", r.UserID, "err", err)
			}
		}
	}
	return int64(len(rows)), nil
}

// CleanupDue удаляет записи об отправленных напоминаниях, срок которых прошёл больше недели назад:
// условия отправки для них уже не выполнятся (воркер, раз в сутки).
func CleanupDue(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	res := db.WithContext(ctx).Exec(`DELETE FROM due_reminders WHERE due_date < ?::date - 7`, now.UTC().Format(time.DateOnly))
	if res.Error != nil {
		return 0, fmt.Errorf("notification: cleanup due: %w", res.Error)
	}
	return res.RowsAffected, nil
}
