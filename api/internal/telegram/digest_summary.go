package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Digest — сообщения бота по расписанию: ежедневная сводка в группу проекта (§8.6) и личные
// напоминания о сроке (§8.7). Проверка раз в минуту из воркера; что уже отправлено — в таблицах
// daily_summaries и due_reminders.
type Digest struct {
	db      *gorm.DB
	out     *Queue
	baseURL string
}

func NewDigest(db *gorm.DB, jobs *asynq.Client, baseURL string) *Digest {
	return &Digest{db: db, out: NewQueue(jobs), baseURL: strings.TrimRight(baseURL, "/")}
}

// SummaryWindow — сводка уходит, если с daily_summary_time по поясу проекта прошло меньше часа:
// воркер, простоявший в это время, отправит её позже, но включённая вечером сводка с утренним
// временем не придёт в тот же вечер.
const SummaryWindow = time.Hour

// MaxOverdue — сколько просроченных задач перечислять в сводке; остальные — числом.
const MaxOverdue = 20

// summaryCandidates — проекты с группой, у которых по их поясу наступило время сводки, а сводки
// за сегодня ещё нет. Настройка daily_summary проверяется в Go (умолчания §8.6).
const summaryCandidates = `SELECT p.id, p.key, p.name, p.telegram_chat_id, p.notification_settings::text AS settings,
		(CAST(@now AS timestamptz) AT TIME ZONE p.timezone)::date AS today
	FROM projects p
	WHERE p.telegram_chat_id IS NOT NULL AND p.deleted_at IS NULL AND NOT p.is_archived
		AND (CAST(@now AS timestamptz) AT TIME ZONE p.timezone) - ((CAST(@now AS timestamptz) AT TIME ZONE p.timezone)::date + p.daily_summary_time)
			BETWEEN interval '0' AND CAST(@window AS interval)
		AND NOT EXISTS (SELECT 1 FROM daily_summaries s WHERE s.project_id = p.id
			AND s.date = (CAST(@now AS timestamptz) AT TIME ZONE p.timezone)::date)`

// Summaries — проход планировщика сводок: каждому подходящему проекту одна сводка за дату.
// Возвращает число отправленных.
func (d *Digest) Summaries(ctx context.Context, now time.Time) (int64, error) {
	var rows []struct {
		ID             uuid.UUID
		Key, Name      string
		TelegramChatID int64
		Settings       string
		Today          time.Time
	}
	if err := d.db.WithContext(ctx).Raw(summaryCandidates, map[string]any{
		"now": now, "window": fmt.Sprintf("%d seconds", int(SummaryWindow.Seconds())),
	}).Scan(&rows).Error; err != nil {
		return 0, fmt.Errorf("telegram: summary candidates: %w", err)
	}
	var sent int64
	for _, p := range rows {
		if !project.EffectiveNotifications(events.JSON(p.Settings))[project.NotifyDailySummary] {
			continue
		}
		text, err := d.summary(ctx, p.ID, p.Key, p.Name, p.Today)
		if err != nil {
			slog.Error("telegram: summary", "project", p.Key, "err", err) // остальные проекты — своим чередом
			continue
		}
		mark := d.db.WithContext(ctx).Exec(`INSERT INTO daily_summaries (project_id, date) VALUES (?, ?) ON CONFLICT DO NOTHING`,
			p.ID, p.Today.Format(time.DateOnly))
		if mark.Error != nil {
			return sent, fmt.Errorf("telegram: summary mark: %w", mark.Error)
		}
		if mark.RowsAffected == 0 {
			continue // другой процесс воркера успел раньше
		}
		if err := d.out.Send(ctx, Message{Op: OpSend, ChatID: p.TelegramChatID, Text: text}, 0); err != nil {
			// Очередь недоступна — снимаем отметку, следующий проход в пределах окна повторит.
			slog.Error("telegram: summary not queued", "project", p.Key, "err", err)
			if err := d.db.WithContext(ctx).Exec(`DELETE FROM daily_summaries WHERE project_id = ? AND date = ?`,
				p.ID, p.Today.Format(time.DateOnly)).Error; err != nil {
				slog.Error("telegram: summary unmark", "project", p.Key, "err", err)
			}
			continue
		}
		sent++
	}
	return sent, nil
}

// summaryData — данные шаблона сводки.
type summaryData struct {
	Key, Name, Date string
	Sprint          *sprintSummary
	Overdue         []overdueTask
	More            int
}

type sprintSummary struct {
	Name, Dates     string
	Statuses        string // «К выполнению: 5 · В работе: 3 · …»
	Spent, Estimate string
}

type overdueTask struct {
	Key, Title, Assignee, Due string
}

// summary — текст сводки: активный спринт как в /sprint (название, даты, задачи по статусам,
// списано из оценки) и просроченные задачи проекта (§3.3: срок раньше сегодняшней даты по поясу
// проекта, задача не закрыта).
func (d *Digest) summary(ctx context.Context, projectID uuid.UUID, key, name string, today time.Time) (string, error) {
	db := d.db.WithContext(ctx)
	data := summaryData{Key: key, Name: name, Date: today.Format("02.01.2006")}

	var sp []struct {
		ID                 uuid.UUID
		Name               string
		StartDate, EndDate *time.Time
	}
	if err := db.Raw(`SELECT id, name, start_date, end_date FROM sprints WHERE project_id = ? AND state = 'active' LIMIT 1`,
		projectID).Scan(&sp).Error; err != nil {
		return "", fmt.Errorf("telegram: summary sprint: %w", err)
	}
	if len(sp) == 1 {
		var st []struct {
			Status   string
			N        int
			Spent    int
			Estimate int
		}
		if err := db.Raw(`SELECT status, count(*) AS n, COALESCE(SUM(spent_minutes), 0) AS spent,
				COALESCE(SUM(estimate_minutes), 0) AS estimate
			FROM tasks WHERE sprint_id = ? AND deleted_at IS NULL GROUP BY status`, sp[0].ID).Scan(&st).Error; err != nil {
			return "", fmt.Errorf("telegram: summary statuses: %w", err)
		}
		counts := map[string]int{}
		spent, estimate := 0, 0
		for _, s := range st {
			counts[s.Status] = s.N
			spent += s.Spent
			if s.Status != task.StatusCancelled {
				estimate += s.Estimate
			}
		}
		var parts []string
		for _, s := range []string{task.StatusTodo, task.StatusInProgress, task.StatusReview, task.StatusDone, task.StatusCancelled} {
			if counts[s] > 0 || s != task.StatusCancelled {
				parts = append(parts, fmt.Sprintf("%s: %d", task.StatusName(s), counts[s]))
			}
		}
		s := &sprintSummary{Name: sp[0].Name, Statuses: strings.Join(parts, " · "),
			Spent: duration.Format(spent), Estimate: duration.Format(estimate)}
		if start, end := sp[0].StartDate, sp[0].EndDate; start != nil && end != nil {
			layout := "02.01"
			if start.Year() != end.Year() {
				layout = "02.01.2006" // спринт через Новый год
			}
			s.Dates = start.Format(layout) + "–" + end.Format("02.01.2006")
		}
		data.Sprint = s
	}

	var overdue []struct {
		Key, Title string
		Assignee   *string
		DueDate    time.Time
	}
	if err := db.Raw(`SELECT t.key, t.title, u.display_name AS assignee, t.due_date FROM tasks t
		LEFT JOIN users u ON u.id = t.assignee_id
		WHERE t.project_id = ? AND t.deleted_at IS NULL AND t.status NOT IN ('done', 'cancelled') AND t.due_date < ?
		ORDER BY t.due_date, t.number LIMIT ?`, projectID, today.Format(time.DateOnly), MaxOverdue+1).Scan(&overdue).Error; err != nil {
		return "", fmt.Errorf("telegram: summary overdue: %w", err)
	}
	if len(overdue) > MaxOverdue {
		var total int
		if err := db.Raw(`SELECT count(*) FROM tasks WHERE project_id = ? AND deleted_at IS NULL
			AND status NOT IN ('done', 'cancelled') AND due_date < ?`, projectID, today.Format(time.DateOnly)).
			Scan(&total).Error; err != nil {
			return "", fmt.Errorf("telegram: summary overdue count: %w", err)
		}
		overdue, data.More = overdue[:MaxOverdue], total-MaxOverdue
	}
	for _, o := range overdue {
		a := "—"
		if o.Assignee != nil {
			a = *o.Assignee
		}
		data.Overdue = append(data.Overdue, overdueTask{Key: o.Key, Title: clip(o.Title, maxTitle), Assignee: a, Due: o.DueDate.Format("02.01.2006")})
	}
	return clip(Text("daily_summary", data), maxMessage), nil
}

// Лимиты длины: Telegram не принимает сообщения длиннее 4096 символов (такое отбрасывается
// без повтора), поэтому названия в списке сокращаются, а весь текст — с запасом.
const (
	maxTitle   = 80
	maxMessage = 4000
)

// clip сокращает s до n символов с многоточием.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// SendDue — личное напоминание о сроке (§8.7) исполнителю, если он привязал Telegram и не
// отключил напоминания (настройка due). Колокольчик (§7.7) создаёт notification.DueReminders.
func (d *Digest) SendDue(ctx context.Context, r notification.Due) error {
	var u []struct {
		TelegramUserID int64
		Settings       string
	}
	if err := d.db.WithContext(ctx).Raw(`SELECT telegram_user_id, notification_settings::text AS settings FROM users
		WHERE id = ? AND is_active AND telegram_user_id IS NOT NULL`, r.UserID).Scan(&u).Error; err != nil {
		return fmt.Errorf("telegram: due recipient: %w", err)
	}
	if len(u) == 0 || !user.EffectiveNotifications(events.JSON(u[0].Settings))[user.NotifyDue] {
		return nil
	}
	tmpl := "p_due_tomorrow"
	if r.Kind == notification.DueOverdue {
		tmpl = "p_overdue"
	}
	text := Text(tmpl, map[string]string{"Key": r.Key, "Title": r.Title, "Date": r.DueDate.Format("02.01.2006")})
	open := models.InlineKeyboardButton{Text: Text("btn_open", nil), URL: d.baseURL + "/tasks/" + r.Key}
	return d.out.Send(ctx, Message{Op: OpSend, ChatID: u[0].TelegramUserID, Text: text, Keyboard: keyboard(open)}, 0)
}

// CleanupSummaries удаляет отметки о сводках старше недели (воркер, раз в сутки).
func (d *Digest) CleanupSummaries(ctx context.Context, now time.Time) (int64, error) {
	res := d.db.WithContext(ctx).Exec(`DELETE FROM daily_summaries WHERE date < ?::date - 7`, now.UTC().Format(time.DateOnly))
	if res.Error != nil {
		return 0, fmt.Errorf("telegram: cleanup summaries: %w", res.Error)
	}
	return res.RowsAffected, nil
}
