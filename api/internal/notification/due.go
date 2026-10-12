package notification

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

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
	y, m, d := now.UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	tasks, err := task.OpenDueBetween(tx, today.AddDate(0, 0, -2), today.AddDate(0, 0, 2))
	if err != nil || len(tasks) == 0 {
		return nil, err
	}
	var projectIDs, userIDs []uuid.UUID
	for _, t := range tasks {
		if !slices.Contains(projectIDs, t.ProjectID) {
			projectIDs = append(projectIDs, t.ProjectID)
		}
		if !slices.Contains(userIDs, t.AssigneeID) {
			userIDs = append(userIDs, t.AssigneeID)
		}
	}
	projects, err := project.LiveBriefs(tx, projectIDs, true)
	if err != nil {
		return nil, err
	}
	zones, err := user.ActiveTimezones(tx, userIDs)
	if err != nil {
		return nil, err
	}
	members, err := project.Memberships(tx, projectIDs, userIDs)
	if err != nil {
		return nil, err
	}
	from, quiet := clockOf(DueHour), clockOf(DueQuietFrom)
	var cand []Due
	for _, t := range tasks {
		p, ok := projects[t.ProjectID]
		tz, active := zones[t.AssigneeID]
		if !ok || !active || !members[project.Membership{ProjectID: t.ProjectID, UserID: t.AssigneeID}] {
			continue
		}
		local, err := inZone(now, tz)
		if err != nil {
			return nil, err
		}
		if c := sinceMidnight(local); c < from || c >= quiet {
			continue
		}
		projectNow, err := inZone(now, p.Timezone)
		if err != nil {
			return nil, err
		}
		r := Due{UserID: t.AssigneeID, TaskID: t.ID, Key: t.Key, Title: t.Title, ProjectID: t.ProjectID,
			ProjectKey: p.Key, DueDate: t.DueDate}
		if t.DueDate.Equal(dateOf(local).AddDate(0, 0, 1)) {
			r.Kind = DueTomorrow
			cand = append(cand, r)
		}
		if t.DueDate.Equal(dateOf(projectNow).AddDate(0, 0, -1)) {
			r.Kind = DueOverdue
			cand = append(cand, r)
		}
	}
	rows := cand[:0]
	for _, r := range cand {
		res := tx.Exec(`INSERT INTO due_reminders (task_id, kind, due_date, user_id) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			r.TaskID, r.Kind, r.DueDate.Format(time.DateOnly), r.UserID)
		if res.Error != nil {
			return nil, fmt.Errorf("notification: due reminders: %w", res.Error)
		}
		if res.RowsAffected == 1 {
			rows = append(rows, r)
		}
	}
	slices.SortFunc(rows, func(a, b Due) int { return cmp.Or(cmp.Compare(a.Key, b.Key), cmp.Compare(a.Kind, b.Kind)) })
	return rows, nil
}

// inZone — момент t по поясу tz.
func inZone(t time.Time, tz string) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("notification: timezone %q: %w", tz, err)
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
