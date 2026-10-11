package telegram_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/user"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}

// summaryMessages — сводки, поставленные в очередь для группы chatID.
func (b *personalBot) summaries(t *testing.T, d *telegram.Digest, now string, chatID int64) []string {
	t.Helper()
	_, err := d.Summaries(context.Background(), at(t, now))
	require.NoError(t, err)
	out := []string{}
	for _, m := range b.messages(t) {
		if m.ChatID == chatID {
			out = append(out, m.Text)
		}
	}
	return out
}

// Сценарий приёмки «ежедневная сводка» (§8.6): менеджер включает сводку и ставит время; в группу
// приходит сводка по активному спринту со списком просроченных задач, один раз в день.
func TestDailySummary(t *testing.T) {
	b := newPersonalBot(t)
	d := telegram.NewDigest(b.g, b.jobs, "https://tasks.example")
	chat := -tgID()
	on, err := project.MergeNotifications(nil, map[string]bool{project.NotifyDailySummary: true})
	require.NoError(t, err)
	require.NoError(t, b.g.Exec(`UPDATE projects SET telegram_chat_id = ?, daily_summary_time = '10:00' WHERE id = ?`,
		chat, b.pr.ID).Error)

	// Сводка выключена по умолчанию (§8.6).
	require.Empty(t, b.summaries(t, d, "2026-10-11T07:00:00Z", chat))
	require.NoError(t, b.g.Exec(`UPDATE projects SET notification_settings = ? WHERE id = ?`, on, b.pr.ID).Error)

	var sprintID string
	require.NoError(t, b.g.Raw(`INSERT INTO sprints (project_id, name, state, start_date, end_date)
		VALUES (?, 'Спринт 7', 'active', '2026-10-05', '2026-10-16') RETURNING id::text`, b.pr.ID).Scan(&sprintID).Error)
	t1, t2, t3 := b.create(t, &b.member.ID), b.create(t, nil), b.create(t, &b.member.ID)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET sprint_id = ?, estimate_minutes = 120 WHERE id IN ?`, sprintID,
		[]any{t1.ID, t2.ID, t3.ID}).Error)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET status = 'in_progress', spent_minutes = 90, due_date = '2026-10-09' WHERE id = ?`, t1.ID).Error)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET status = 'done', spent_minutes = 60, due_date = '2026-10-01' WHERE id = ?`, t3.ID).Error)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET due_date = '2026-10-10' WHERE id = ?`, t2.ID).Error)
	t4 := b.create(t, nil) // в бэклоге, срок сегодня — не просрочена
	require.NoError(t, b.g.Exec(`UPDATE tasks SET due_date = '2026-10-11' WHERE id = ?`, t4.ID).Error)

	require.Empty(t, b.summaries(t, d, "2026-10-11T06:59:00Z", chat), "09:59 по Москве — рано")
	got := b.summaries(t, d, "2026-10-11T07:00:00Z", chat)
	require.Equal(t, []string{"📊 Сводка " + b.pr.Key + " «Тайм-менеджмент» на 11.10.2026\n\n" +
		"🏃 Спринт 7: 05.10–16.10.2026\n" +
		"К выполнению: 1 · В работе: 1 · На проверке: 0 · Готово: 1\n" +
		"Списано 2h 30m из 6h\n\n" +
		"⚠️ Просрочено:\n" +
		t1.Key + " «Экспорт <CSV>» — Иван Петров, срок 09.10.2026\n" +
		t2.Key + " «Экспорт <CSV>» — —, срок 10.10.2026"}, got)
	require.Empty(t, b.summaries(t, d, "2026-10-11T07:30:00Z", chat), "один раз в день")

	// Без активного спринта и просрочек; позже часа после времени сводки — не отправляется.
	require.NoError(t, b.g.Exec(`UPDATE sprints SET state = 'closed' WHERE id = ?`, sprintID).Error)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET due_date = NULL WHERE project_id = ?`, b.pr.ID).Error)
	require.Empty(t, b.summaries(t, d, "2026-10-12T08:01:00Z", chat))
	require.Equal(t, []string{"📊 Сводка " + b.pr.Key + " «Тайм-менеджмент» на 13.10.2026\n\nНет активного спринта\n\nПросроченных задач нет"},
		b.summaries(t, d, "2026-10-13T07:10:00Z", chat))

	// Архивный проект сводку не получает.
	require.NoError(t, b.g.Exec(`UPDATE projects SET is_archived = true WHERE id = ?`, b.pr.ID).Error)
	require.Empty(t, b.summaries(t, d, "2026-10-14T07:10:00Z", chat))

	n, err := d.CleanupSummaries(context.Background(), at(t, "2026-10-30T00:00:00Z"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(2))
}

// Длинный список просрочек обрезается, остаток — числом.
func TestDailySummaryManyOverdue(t *testing.T) {
	b := newPersonalBot(t)
	d := telegram.NewDigest(b.g, b.jobs, "https://tasks.example")
	chat := -tgID()
	on, err := project.MergeNotifications(nil, map[string]bool{project.NotifyDailySummary: true})
	require.NoError(t, err)
	require.NoError(t, b.g.Exec(`UPDATE projects SET telegram_chat_id = ?, notification_settings = ? WHERE id = ?`, chat, on, b.pr.ID).Error)
	for range telegram.MaxOverdue + 2 {
		tk := b.create(t, nil)
		require.NoError(t, b.g.Exec(`UPDATE tasks SET due_date = '2026-10-01' WHERE id = ?`, tk.ID).Error)
	}
	got := b.summaries(t, d, "2026-10-11T07:00:00Z", chat)
	require.Len(t, got, 1)
	require.Contains(t, got[0], "\nи ещё 2")
}

// Сценарий приёмки «напоминание о сроке»: личное сообщение исполнителю с кнопкой «Открыть»,
// если Telegram привязан и напоминания не отключены.
func TestSendDue(t *testing.T) {
	b := newPersonalBot(t)
	d := telegram.NewDigest(b.g, b.jobs, "https://tasks.example/")
	tk := b.create(t, &b.member.ID)
	b.dispatch(t)
	b.messages(t)
	due := notification.Due{UserID: b.member.ID, TaskID: tk.ID, Kind: notification.DueTomorrow, Key: tk.Key, Title: tk.Title,
		DueDate: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}
	require.NoError(t, d.SendDue(context.Background(), due))
	due.Kind = notification.DueOverdue
	require.NoError(t, d.SendDue(context.Background(), due))
	msgs := b.messages(t)
	require.Equal(t, []string{
		"⏰ Срок " + tk.Key + " «Экспорт <CSV>» завтра, 12.10.2026",
		"⚠️ Срок " + tk.Key + " «Экспорт <CSV>» просрочен: 12.10.2026",
	}, texts(msgs))
	require.Equal(t, b.tg[b.member.ID], msgs[0].ChatID)
	require.Equal(t, []string{"Открыть|https://tasks.example/tasks/" + tk.Key}, buttons(msgs[0]))

	off, err := user.MergeNotifications(nil, map[string]bool{user.NotifyDue: false})
	require.NoError(t, err)
	require.NoError(t, b.g.Model(b.member).Update("notification_settings", off).Error)
	require.NoError(t, d.SendDue(context.Background(), due))
	due.UserID = b.admin.ID // без Telegram
	require.NoError(t, d.SendDue(context.Background(), due))
	require.Empty(t, b.messages(t))
}
