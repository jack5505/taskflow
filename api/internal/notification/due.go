package notification

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
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
// due_reminders на задачу, вид, срок и получателя; уже записанные не возвращаются). Получатель —
// активный исполнитель открытой задачи, участник проекта; проект не удалён и не в архиве. С 09:00
// до 22:00 по поясу пользователя:
//   - «срок завтра» — срок равен завтрашней дате пользователя;
//   - «просрочено» — в день просрочки: срок равен вчерашней дате по поясу проекта (задача
//     просрочена по поясу проекта, §3.3), один раз.
//
// Если воркер стоял в 09:00, напоминание уходит, как только он поднимется в тот же день.
func dueCandidates(tx *gorm.DB, now time.Time) ([]Due, error) {
	// Грубая граница по дате UTC: пояса расходятся меньше чем на двое суток, а индекс
	// tasks_open_due_idx отсекает остальные задачи.
	today := dateOf(now.UTC())
	tasks, err := task.OpenDueBetween(tx, today.AddDate(0, 0, -2), today.AddDate(0, 0, 2))
	if err != nil || len(tasks) == 0 {
		return nil, err
	}
	zones, err := user.ActiveTimezones(tx, uniq(tasks, func(t task.DueTask) uuid.UUID { return t.AssigneeID }))
	if err != nil {
		return nil, err
	}
	// Сначала окно 09:00–22:00 по поясу исполнителя: почти на каждом проходе дальше идти некуда.
	from, quiet := clockOf(DueHour), clockOf(DueQuietFrom)
	userNow := map[uuid.UUID]time.Time{}
	inWindow := tasks[:0]
	for _, t := range tasks {
		tz, active := zones[t.AssigneeID]
		if !active {
			continue
		}
		local, err := inZone(now, tz)
		if err != nil {
			return nil, err
		}
		if c := sinceMidnight(local); c >= from && c < quiet {
			inWindow, userNow[t.AssigneeID] = append(inWindow, t), local
		}
	}
	if len(inWindow) == 0 {
		return nil, nil
	}
	projectIDs := uniq(inWindow, func(t task.DueTask) uuid.UUID { return t.ProjectID })
	projects, err := project.LiveBriefs(tx, projectIDs, true)
	if err != nil {
		return nil, err
	}
	members, err := project.Memberships(tx, projectIDs, uniq(inWindow, func(t task.DueTask) uuid.UUID { return t.AssigneeID }))
	if err != nil {
		return nil, err
	}
	var cand []Due
	for _, t := range inWindow {
		p, ok := projects[t.ProjectID]
		if !ok || !members[project.Membership{ProjectID: t.ProjectID, UserID: t.AssigneeID}] {
			continue
		}
		projectNow, err := inZone(now, p.Timezone)
		if err != nil {
			return nil, err
		}
		r := Due{UserID: t.AssigneeID, TaskID: t.ID, Key: t.Key, Title: t.Title, ProjectID: t.ProjectID,
			ProjectKey: p.Key, DueDate: t.DueDate}
		if t.DueDate.Equal(dateOf(userNow[t.AssigneeID]).AddDate(0, 0, 1)) {
			r.Kind = DueTomorrow
			cand = append(cand, r)
		}
		if t.DueDate.Equal(dateOf(projectNow).AddDate(0, 0, -1)) {
			r.Kind = DueOverdue
			cand = append(cand, r)
		}
	}
	return recordDue(tx, cand)
}

// recordDue записывает напоминания cand в due_reminders одним запросом и возвращает только
// записанные (уже отправленные раньше отсеиваются ON CONFLICT) по ключу задачи и виду.
func recordDue(tx *gorm.DB, cand []Due) ([]Due, error) {
	if len(cand) == 0 {
		return nil, nil
	}
	values := make([]string, len(cand))
	args := make([]any, 0, 4*len(cand))
	for i, r := range cand {
		values[i] = "(?, ?, ?, ?)"
		args = append(args, r.TaskID, r.Kind, r.DueDate.Format(time.DateOnly), r.UserID)
	}
	var inserted []struct {
		TaskID uuid.UUID
		Kind   string
	}
	if err := tx.Raw(`INSERT INTO due_reminders (task_id, kind, due_date, user_id) VALUES `+strings.Join(values, ", ")+`
		ON CONFLICT DO NOTHING RETURNING task_id, kind`, args...).Scan(&inserted).Error; err != nil {
		return nil, fmt.Errorf("notification: due reminders: %w", err)
	}
	type key struct {
		task uuid.UUID
		kind string
	}
	fresh := make(map[key]bool, len(inserted))
	for _, r := range inserted {
		fresh[key{r.TaskID, r.Kind}] = true
	}
	rows := cand[:0]
	for _, r := range cand {
		if fresh[key{r.TaskID, r.Kind}] {
			rows = append(rows, r)
		}
	}
	slices.SortFunc(rows, func(a, b Due) int { return cmp.Or(cmp.Compare(a.Key, b.Key), cmp.Compare(a.Kind, b.Kind)) })
	return rows, nil
}

// uniq — значения key элементов items без повторов, в порядке первого появления.
func uniq[T any](items []T, key func(T) uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(items))
	var out []uuid.UUID
	for _, it := range items {
		if k := key(it); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// inZone — момент t по поясу tz.
func inZone(t time.Time, tz string) (time.Time, error) {
	loc, err := model.Location(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("notification: %w", err)
	}
	return t.In(loc), nil
}

// dateOf — дата местного времени t (полночь UTC этой даты, как даты из базы).
func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// sinceMidnight — время суток местного t.
func sinceMidnight(t time.Time) time.Duration {
	h, m, s := t.Clock()
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second +
		time.Duration(t.Nanosecond())
}

// clockOf — «ЧЧ:ММ» как время суток.
func clockOf(hhmm string) time.Duration {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic(err) // константы пакета
	}
	return sinceMidnight(t)
}

// DueReminders — проход планировщика напоминаний (§8.7, §7.7): находит задачи со сроком завтра
// и просроченные сегодня, создаёт уведомления колокольчика типа due и после фиксации
// транзакции передаёт каждое напоминание в send (личное сообщение бота; nil — бот выключен).
// Ошибка send только пишется в лог: напоминание уже записано и в колокольчике есть.
func DueReminders(ctx context.Context, db *gorm.DB, now time.Time, send func(context.Context, Due) error) (int64, error) {
	var rows []Due
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if rows, err = dueCandidates(tx, now); err != nil {
			return err
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
