package telegram_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

// listBot — группа с двумя проектами (TM — b.pr и второй), команды /tasks и /sprint.
type listBot struct {
	*cmdBot
	other *project.Project
	chat  int64
}

func newListBot(t *testing.T) *listBot {
	t.Helper()
	b := &listBot{cmdBot: newCmdBot(t, func(rt *telegram.Router) {
		g := testutil.SharedDB(t)
		groups, projects := telegram.NewGroups(g, "TaskBot"), project.NewService(g)
		telegram.RegisterGroupLists(rt, g, groups)
		jobs := queue.NewClient(testutil.SharedRedisAddr(t))
		t.Cleanup(func() { _ = jobs.Close() })
		cards := telegram.NewCards(g, telegram.NewQueue(jobs), "https://tasks.example/")
		telegram.RegisterTake(rt, telegram.NewTaskCommands(g, task.NewService(g, projects), projects, groups, cards))
	})}
	var err error
	b.other, err = b.projects.Create(context.Background(), project.CreateInput{Key: testutil.UniqueKey(t), Name: "Второй"})
	require.NoError(t, err)
	a, err := b.projects.Access(context.Background(), b.admin, b.other.Key)
	require.NoError(t, err)
	_, _, err = b.projects.PutMember(audit.WithActor(context.Background(), b.admin.ID), a, b.manager.ID, project.RoleManager)
	require.NoError(t, err)
	b.chat = -100_000_000 - testutil.Next()
	require.NoError(t, b.g.Exec(`UPDATE projects SET telegram_chat_id = ? WHERE id IN ?`, b.chat, []any{b.pr.ID, b.other.ID}).Error)
	return b
}

// sprint — активный спринт проекта p.
func (b *listBot) sprint(t *testing.T, p *project.Project, name string) string {
	t.Helper()
	var id string
	require.NoError(t, b.g.Raw(`INSERT INTO sprints (project_id, name, state, start_date, end_date)
		VALUES (?, ?, 'active', '2026-10-05', '2026-10-16') RETURNING id::text`, p.ID, name).Scan(&id).Error)
	return id
}

// task — задача проекта p в спринте sprintID (пусто — бэклог) с приоритетом priority.
func (b *listBot) task(t *testing.T, p *project.Project, sprintID, title, priority string) *task.Task {
	t.Helper()
	a, err := b.projects.Access(context.Background(), b.manager, p.Key)
	require.NoError(t, err)
	tk, err := b.tasks.Create(context.Background(), a, b.manager, task.CreateInput{Title: title, Priority: priority})
	require.NoError(t, err)
	if sprintID != "" {
		require.NoError(t, b.g.Exec(`UPDATE tasks SET sprint_id = ? WHERE id = ?`, sprintID, tk.ID).Error)
	}
	return tk
}

// byKey — разделы двух проектов в порядке ключей (как ChatProjects), через пустую строку.
func (b *listBot) byKey(pr, other string) string {
	if b.other.Key < b.pr.Key {
		pr, other = other, pr
	}
	return pr + "\n\n" + other
}

func (b *listBot) cmd(t *testing.T, text string) []telegram.Message {
	t.Helper()
	upd := msgUpdate(b.chat, models.ChatTypeSupergroup, text)
	upd.Message.From.ID = b.tg[b.member.ID]
	b.p.Handle(context.Background(), upd, time.Now())
	return b.messages(t)
}

// Приёмка §8.5 «группа с двумя проектами»: /tasks — свободные задачи активных спринтов по обоим
// проектам с разбивкой и кнопками «Взять», по приоритету; /tasks <ключ> — по одному.
func TestTasksCommandTwoProjects(t *testing.T) {
	b := newListBot(t)
	s1 := b.sprint(t, b.pr, "Спринт 7")
	low := b.task(t, b.pr, s1, "Низкая", "low")
	crit := b.task(t, b.pr, s1, "Критическая", "critical")
	taken := b.task(t, b.pr, s1, "Взятая", "high")
	require.NoError(t, b.g.Exec(`UPDATE tasks SET assignee_id = ?, status = 'in_progress' WHERE id = ?`, b.member.ID, taken.ID).Error)
	b.task(t, b.pr, "", "В бэклоге", "critical")

	msgs := b.cmd(t, "/tasks")
	require.Len(t, msgs, 1)
	require.Equal(t, b.byKey("📋 Свободные задачи "+b.pr.Key+" «Тайм-менеджмент»:\n"+
		crit.Key+" «Критическая» · Критический\n"+
		low.Key+" «Низкая» · Низкий",
		"📋 Свободные задачи "+b.other.Key+" «Второй»: нет активного спринта"), msgs[0].Text)
	require.Equal(t, 7, msgs[0].ReplyTo, "ответом на команду")
	require.Equal(t, [][]models.InlineKeyboardButton{
		{{Text: "Взять " + crit.Key, CallbackData: "take:" + crit.ID.String()}},
		{{Text: "Взять " + low.Key, CallbackData: "take:" + low.ID.String()}},
	}, msgs[0].Keyboard.InlineKeyboard)

	// Второй проект со спринтом без свободных задач; /tasks <ключ> — один проект.
	b.sprint(t, b.other, "Спринт 1")
	msgs = b.cmd(t, "/tasks "+strings.ToLower(b.other.Key))
	require.Equal(t, "📋 Свободные задачи "+b.other.Key+" «Второй»: нет", msgs[0].Text)
	require.Nil(t, msgs[0].Keyboard)

	// Архивный проект: задачи не предлагаются.
	require.NoError(t, b.g.Exec(`UPDATE projects SET is_archived = true WHERE id = ?`, b.pr.ID).Error)
	msgs = b.cmd(t, "/tasks "+b.pr.Key)
	require.Equal(t, "📋 Свободные задачи "+b.pr.Key+" «Тайм-менеджмент»: проект в архиве", msgs[0].Text)
	require.Nil(t, msgs[0].Keyboard)
}

// §8.5: /tasks — не больше 20 задач на все проекты, самые приоритетные; у проекта — сколько его
// задач не поместилось, даже если не показана ни одна.
func TestTasksCommandLimit(t *testing.T) {
	b := newListBot(t)
	s1, s2 := b.sprint(t, b.pr, "Спринт 7"), b.sprint(t, b.other, "Спринт 1")
	for i := range 3 {
		b.task(t, b.pr, s1, fmt.Sprintf("Первый %d", i), "low")
	}
	for i := range 22 {
		b.task(t, b.other, s2, fmt.Sprintf("Второй %d", i), "high")
	}
	msgs := b.cmd(t, "/tasks")
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Keyboard.InlineKeyboard, telegram.MaxFreeTasks)
	require.Equal(t, 20, strings.Count(msgs[0].Text, "· Высокий"))
	require.Contains(t, msgs[0].Text, "📋 Свободные задачи "+b.pr.Key+" «Тайм-менеджмент»:\n…и ещё 3: /tasks "+b.pr.Key)
	require.Contains(t, msgs[0].Text, "\n…и ещё 2: /tasks "+b.other.Key)

	// Длинные названия: текст не длиннее лимита, кнопки — только у показанных задач.
	require.NoError(t, b.g.Exec(`UPDATE tasks SET title = ? WHERE project_id = ?`, strings.Repeat("Д", 200), b.other.ID).Error)
	require.NoError(t, b.g.Exec(`UPDATE projects SET name = ? WHERE id IN ?`, strings.Repeat("Н", 100), []any{b.pr.ID, b.other.ID}).Error)
	msgs = b.cmd(t, "/tasks "+b.other.Key+" лишнее")
	require.LessOrEqual(t, len([]rune(msgs[0].Text)), 4000)
	require.Equal(t, strings.Count(msgs[0].Text, "· Высокий"), len(msgs[0].Keyboard.InlineKeyboard))
}

// Приёмка §8.5: /sprint — сводка активного спринта по каждому проекту группы одним сообщением.
func TestSprintCommandTwoProjects(t *testing.T) {
	b := newListBot(t)
	s1 := b.sprint(t, b.pr, "Спринт 7")
	t1 := b.task(t, b.pr, s1, "Первая", "")
	b.task(t, b.pr, s1, "Вторая", "")
	require.NoError(t, b.g.Exec(`UPDATE tasks SET estimate_minutes = 180 WHERE sprint_id = ?`, s1).Error)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET status = 'in_progress', assignee_id = ?, spent_minutes = 90 WHERE id = ?`, b.member.ID, t1.ID).Error)

	want1 := "📊 " + b.pr.Key + " «Тайм-менеджмент»\n🏃 Спринт 7: 05.10–16.10.2026\n" +
		"К выполнению: 1 · В работе: 1 · На проверке: 0 · Готово: 0\nСписано 1h 30m из 6h"
	msgs := b.cmd(t, "/sprint")
	require.Len(t, msgs, 1)
	require.Equal(t, b.byKey(want1, "📊 "+b.other.Key+" «Второй»\nНет активного спринта"), msgs[0].Text)

	msgs = b.cmd(t, "/sprint@TaskBot "+b.pr.Key)
	require.Equal(t, want1, msgs[0].Text)
}

// /tasks и /sprint — команды группы: в личке, в группе без проектов и с чужим ключом — подсказки.
func TestGroupListsErrors(t *testing.T) {
	b := newListBot(t)
	for _, cmd := range []string{"/tasks", "/sprint"} {
		require.Equal(t, []string{"Эта команда работает в группе проекта"}, b.say(t, b.member, cmd))
		require.Equal(t, []string{"К этой группе не подключён ни один проект"},
			b.sayIn(t, b.member, -100_000_000-testutil.Next(), models.ChatTypeGroup, cmd))
		require.Equal(t, []string{"Проект ZZZZ не подключён к этой группе"}, textsOf(b.cmd(t, cmd+" zzzz")))
	}
}

func textsOf(msgs []telegram.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Text)
	}
	return out
}

// Кнопка «Взять» под списком /tasks берёт задачу в работу (§8.4, обработчик take:<task_id>).
func TestTasksCommandTakeButton(t *testing.T) {
	b := newListBot(t)
	tk := b.task(t, b.pr, b.sprint(t, b.pr, "Спринт 7"), "Экспорт", "high")
	msgs := b.cmd(t, "/tasks "+b.pr.Key)
	require.Len(t, msgs[0].Keyboard.InlineKeyboard, 1)
	button := msgs[0].Keyboard.InlineKeyboard[0][0]

	upd := callbackUpdate(b.chat, button.CallbackData)
	upd.CallbackQuery.From.ID = b.tg[b.member.ID]
	b.p.Handle(context.Background(), upd, time.Now())
	b.messages(t)

	var got task.Task
	require.NoError(t, b.g.First(&got, "id = ?", tk.ID).Error)
	require.Equal(t, task.StatusInProgress, got.Status)
	require.Equal(t, b.member.ID, *got.AssigneeID)
	require.Len(t, b.env.tg.Calls("answerCallbackQuery"), 1)

	// После взятия задача из списка уходит.
	msgs = b.cmd(t, "/tasks "+b.pr.Key)
	require.Equal(t, "📋 Свободные задачи "+b.pr.Key+" «Тайм-менеджмент»: нет", msgs[0].Text)
}
