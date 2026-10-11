package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// dueFixture — проект (пояс Москвы) с менеджером и исполнителем.
type dueFixture struct {
	g        *gorm.DB
	projects *project.Service
	tasks    *task.Service
	pr       *project.Project
	manager  *user.User
	member   *user.User
	sent     []notification.Due
}

func newDueFixture(t *testing.T, memberTZ string) *dueFixture {
	t.Helper()
	g := testutil.SharedDB(t)
	f := &dueFixture{g: g, projects: project.NewService(g)}
	f.tasks = task.NewService(g, f.projects)
	admin := newUser(t, g)
	require.NoError(t, g.Model(admin).Update("is_system_admin", true).Error)
	admin.IsSystemAdmin = true
	var err error
	f.pr, err = f.projects.Create(context.Background(), project.CreateInput{Key: testutil.UniqueKey(t), Name: "Проект"})
	require.NoError(t, err)
	f.manager, f.member = newUser(t, g), newUser(t, g)
	require.NoError(t, g.Model(f.member).Update("timezone", memberTZ).Error)
	a, err := f.projects.Access(context.Background(), admin, f.pr.Key)
	require.NoError(t, err)
	ctx := audit.WithActor(context.Background(), admin.ID)
	_, _, err = f.projects.PutMember(ctx, a, f.manager.ID, project.RoleManager)
	require.NoError(t, err)
	_, _, err = f.projects.PutMember(ctx, a, f.member.ID, project.RoleMember)
	require.NoError(t, err)
	return f
}

// task — задача исполнителя со сроком due.
func (f *dueFixture) task(t *testing.T, due string) *task.Task {
	t.Helper()
	a, err := f.projects.Access(context.Background(), f.manager, f.pr.Key)
	require.NoError(t, err)
	tk, err := f.tasks.Create(context.Background(), a, f.manager, task.CreateInput{Title: "Отчёт", AssigneeID: &f.member.ID})
	require.NoError(t, err)
	require.NoError(t, f.g.Exec(`UPDATE tasks SET due_date = ? WHERE id = ?`, due, tk.ID).Error)
	return tk
}

// run — проход планировщика в момент at (UTC); напоминания этого проекта.
func (f *dueFixture) run(t *testing.T, at string) []notification.Due {
	t.Helper()
	now, err := time.Parse(time.RFC3339, at)
	require.NoError(t, err)
	f.sent = nil
	_, err = notification.DueReminders(context.Background(), f.g, now, func(_ context.Context, d notification.Due) error {
		if d.ProjectID == f.pr.ID {
			f.sent = append(f.sent, d)
		}
		return errors.New("telegram недоступен") // ошибка отправки не отменяет напоминание
	})
	require.NoError(t, err)
	return f.sent
}

func kinds(ds []notification.Due) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.Key+" "+d.Kind)
	}
	return out
}

// §8.7, §7.7: «срок завтра» — не раньше 09:00 по поясу исполнителя, один раз; «просрочено» — в
// день просрочки по поясу проекта, один раз. Оба — в колокольчик (тип due).
func TestDueReminders(t *testing.T) {
	f := newDueFixture(t, "Europe/Moscow")
	tk := f.task(t, "2026-10-12")

	require.Empty(t, f.run(t, "2026-10-10T19:30:00Z"), "22:30 по Москве накануне — ночью не будим")
	require.Empty(t, f.run(t, "2026-10-11T05:59:00Z"), "08:59 по Москве — рано")
	require.Equal(t, []string{tk.Key + " due_tomorrow"}, kinds(f.run(t, "2026-10-11T06:00:00Z")))
	require.Empty(t, f.run(t, "2026-10-11T12:00:00Z"), "один раз")
	require.Empty(t, f.run(t, "2026-10-12T08:00:00Z"), "в день срока — ничего")
	require.Empty(t, f.run(t, "2026-10-13T05:00:00Z"), "просрочено, но у исполнителя ещё нет 09:00")
	require.Equal(t, []string{tk.Key + " overdue"}, kinds(f.run(t, "2026-10-13T06:00:00Z")))
	require.Empty(t, f.run(t, "2026-10-13T20:00:00Z"), "ровно один раз")
	require.Empty(t, f.run(t, "2026-10-14T08:00:00Z"), "только в день просрочки")

	var rows []struct {
		TaskID  uuid.UUID
		Payload string
	}
	require.NoError(t, f.g.Raw(`SELECT task_id, payload::text AS payload FROM notifications WHERE user_id = ? AND type = ?
		ORDER BY created_at`, f.member.ID, notification.TypeDue).Scan(&rows).Error)
	require.Len(t, rows, 2)
	var p map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[0].Payload), &p))
	require.Equal(t, map[string]any{"kind": "due_tomorrow", "key": tk.Key, "title": "Отчёт", "due_date": "2026-10-12",
		"project_id": f.pr.ID.String(), "project_key": f.pr.Key}, p)
	require.Equal(t, tk.ID, rows[1].TaskID)

	// Срок перенесли — напоминание по новому сроку придёт снова.
	require.NoError(t, f.g.Exec(`UPDATE tasks SET due_date = '2026-10-15' WHERE id = ?`, tk.ID).Error)
	require.Equal(t, []string{tk.Key + " due_tomorrow"}, kinds(f.run(t, "2026-10-14T09:00:00Z")))

	// Уборка: записи со сроком больше недели назад.
	n, err := notification.CleanupDue(context.Background(), f.g, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1))
	var left int64
	require.NoError(t, f.g.Raw(`SELECT count(*) FROM due_reminders WHERE task_id = ?`, tk.ID).Scan(&left).Error)
	require.EqualValues(t, 1, left, "срок 15.10 ещё в пределах недели")
}

// 09:00 считается по поясу исполнителя, а не сервера: в Токио оно наступает в 00:00 UTC.
func TestDueRemindersUserTimezone(t *testing.T) {
	f := newDueFixture(t, "Asia/Tokyo")
	tk := f.task(t, "2026-10-12")
	require.Empty(t, f.run(t, "2026-10-10T23:59:00Z"))
	require.Equal(t, []string{tk.Key + " due_tomorrow"}, kinds(f.run(t, "2026-10-11T00:00:00Z")))
}

// Без напоминаний: закрытые, удалённые, без исполнителя, архивный проект, деактивированный исполнитель.
func TestDueRemindersSkips(t *testing.T) {
	f := newDueFixture(t, "Europe/Moscow")
	done := f.task(t, "2026-10-12")
	require.NoError(t, f.g.Exec(`UPDATE tasks SET status = 'done' WHERE id = ?`, done.ID).Error)
	deleted := f.task(t, "2026-10-12")
	require.NoError(t, f.g.Exec(`UPDATE tasks SET deleted_at = now() WHERE id = ?`, deleted.ID).Error)
	free := f.task(t, "2026-10-12")
	require.NoError(t, f.g.Exec(`UPDATE tasks SET assignee_id = NULL WHERE id = ?`, free.ID).Error)
	require.Empty(t, f.run(t, "2026-10-11T07:00:00Z"))

	f.task(t, "2026-10-12")
	require.NoError(t, f.g.Exec(`UPDATE projects SET is_archived = true WHERE id = ?`, f.pr.ID).Error)
	require.Empty(t, f.run(t, "2026-10-11T07:00:00Z"), "архивный проект")
	require.NoError(t, f.g.Exec(`UPDATE projects SET is_archived = false WHERE id = ?`, f.pr.ID).Error)
	require.NoError(t, f.g.Exec(`UPDATE users SET is_active = false WHERE id = ?`, f.member.ID).Error)
	require.Empty(t, f.run(t, "2026-10-11T07:00:00Z"), "деактивированный не получает уведомлений (§3.1)")
}
