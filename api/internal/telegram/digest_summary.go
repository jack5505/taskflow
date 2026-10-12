package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"

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

// Summaries — проход планировщика сводок: каждому подходящему проекту одна сводка за дату.
// Возвращает число отправленных.
func (d *Digest) Summaries(ctx context.Context, now time.Time) (int64, error) {
	rows, err := project.SummaryCandidates(d.db.WithContext(ctx), now, SummaryWindow)
	if err != nil {
		return 0, err
	}
	done, err := d.summarized(ctx, rows)
	if err != nil {
		return 0, err
	}
	var sent int64
	for _, p := range rows {
		if done[p.ID] || !project.EffectiveNotifications(p.Settings)[project.NotifyDailySummary] {
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

// summarized — проекты из ps, которым сводка за их сегодняшнюю дату уже отправлена.
func (d *Digest) summarized(ctx context.Context, ps []project.SummaryCandidate) (map[uuid.UUID]bool, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	today := make(map[uuid.UUID]string, len(ps))
	ids := make([]uuid.UUID, len(ps))
	var dates []string // у проектов в разных поясах «сегодня» может различаться
	for i, p := range ps {
		day := p.Today.Format(time.DateOnly)
		ids[i], today[p.ID] = p.ID, day
		if !slices.Contains(dates, day) {
			dates = append(dates, day)
		}
	}
	var rows []struct {
		ProjectID uuid.UUID
		Date      time.Time
	}
	if err := d.db.WithContext(ctx).Raw(`SELECT project_id, date FROM daily_summaries WHERE project_id IN ? AND date IN ?`,
		ids, dates).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("telegram: sent summaries: %w", err)
	}
	done := map[uuid.UUID]bool{}
	for _, r := range rows {
		if r.Date.Format(time.DateOnly) == today[r.ProjectID] {
			done[r.ProjectID] = true
		}
	}
	return done, nil
}

// summaryData — данные шаблона сводки.
type summaryData struct {
	Key, Name, Date string
	Sprint          *sprintSummary
	Overdue         []overdueTask
	More            int
}

type overdueTask struct {
	Key, Title, Assignee, Due string
}

// summary — текст сводки: активный спринт как в /sprint (activeSprint) и просроченные задачи проекта (§3.3: срок раньше сегодняшней даты по поясу
// проекта, задача не закрыта).
func (d *Digest) summary(ctx context.Context, projectID uuid.UUID, key, name string, today time.Time) (string, error) {
	db := d.db.WithContext(ctx)
	data := summaryData{Key: key, Name: name, Date: today.Format("02.01.2006")}

	sprint, err := activeSprint(db, projectID)
	if err != nil {
		return "", err
	}
	data.Sprint = sprint

	overdue, err := task.OverdueIn(db, projectID, today, MaxOverdue+1)
	if err != nil {
		return "", err
	}
	if len(overdue) > MaxOverdue {
		total, err := task.OverdueCount(db, projectID, today)
		if err != nil {
			return "", err
		}
		overdue, data.More = overdue[:MaxOverdue], total-MaxOverdue
	}
	var assignees []uuid.UUID
	for _, o := range overdue {
		if o.AssigneeID != nil {
			assignees = append(assignees, *o.AssigneeID)
		}
	}
	names, err := user.Names(db, assignees...)
	if err != nil {
		return "", err
	}
	for _, o := range overdue {
		a := "—"
		if o.AssigneeID != nil {
			if n, ok := names[*o.AssigneeID]; ok {
				a = n.DisplayName
			}
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
	u, err := user.TelegramRecipients(d.db.WithContext(ctx), []uuid.UUID{r.UserID})
	if err != nil {
		return err
	}
	if len(u) == 0 || !user.EffectiveNotifications(u[0].Settings)[user.NotifyDue] {
		return nil
	}
	tmpl := "p_due_tomorrow"
	if r.Kind == notification.DueOverdue {
		tmpl = "p_overdue"
	}
	text := Text(tmpl, map[string]string{"Key": r.Key, "Title": r.Title, "Date": r.DueDate.Format("02.01.2006")})
	open := models.InlineKeyboardButton{Text: Text("btn_open", nil), URL: d.baseURL + "/tasks/" + r.Key}
	return d.out.Send(ctx, Message{Op: OpSend, ChatID: u[0].ChatID, Text: text, Keyboard: keyboard(open)}, 0)
}

// CleanupSummaries удаляет отметки о сводках старше недели (воркер, раз в сутки).
func (d *Digest) CleanupSummaries(ctx context.Context, now time.Time) (int64, error) {
	res := d.db.WithContext(ctx).Exec(`DELETE FROM daily_summaries WHERE date < ?::date - 7`, now.UTC().Format(time.DateOnly))
	if res.Error != nil {
		return 0, fmt.Errorf("telegram: cleanup summaries: %w", res.Error)
	}
	return res.RowsAffected, nil
}
