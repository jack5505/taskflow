package telegram_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

// newBot — личные команды, /new и карточки задач (событие task.created) на одном роутере.
type newBot struct {
	*cmdBot
	d    *events.Dispatcher
	seen map[string]bool
}

func newNewBot(t *testing.T) *newBot {
	t.Helper()
	b := &newBot{d: events.NewDispatcher(nil), seen: map[string]bool{}}
	var router *telegram.Router
	g := testutil.SharedDB(t)
	b.cmdBot = newCmdBot(t, func(rt *telegram.Router) {
		telegram.RegisterNewTask(rt, project.NewService(g), task.NewService(g, project.NewService(g)),
			telegram.NewGroups(g, "TaskBot"), "https://tasks.example/")
		router = rt
	})
	telegram.RegisterCards(router, b.d, telegram.NewCards(g, b.out, "https://tasks.example/"))
	return b
}

// bindGroup подключает проекты к группе chatID (как /bind, без кода).
func (b *newBot) bindGroup(t *testing.T, chatID int64, ps ...*project.Project) {
	t.Helper()
	for _, p := range ps {
		require.NoError(t, b.g.Exec(`UPDATE projects SET telegram_chat_id = ? WHERE id = ?`, chatID, p.ID).Error)
	}
}

// second — ещё один проект, где b.member участник.
func (b *newBot) second(t *testing.T) *project.Project {
	t.Helper()
	p, err := b.projects.Create(context.Background(), project.CreateInput{Key: testutil.UniqueKey(t), Name: "Второй"})
	require.NoError(t, err)
	a, err := b.projects.Access(context.Background(), b.admin, p.Key)
	require.NoError(t, err)
	_, _, err = b.projects.PutMember(audit.WithActor(context.Background(), b.admin.ID), a, b.member.ID, project.RoleMember)
	require.NoError(t, err)
	return p
}

// last — последняя задача проекта p.
func (b *newBot) last(t *testing.T, p *project.Project) *task.Task {
	t.Helper()
	var tk task.Task
	require.NoError(t, b.g.Where("project_id = ?", p.ID).Order("number DESC").First(&tk).Error)
	return &tk
}

// cardQueued — событие task.created задачи tk поставило карточку в группу (§8.3).
func (b *newBot) cardQueued(t *testing.T, tk *task.Task) bool {
	t.Helper()
	var rows []struct {
		ID      string
		Type    string
		Payload string
	}
	require.NoError(t, b.g.Raw(`SELECT id, type, payload::text AS payload FROM outbox_events
		WHERE type = ? AND payload->>'task_id' = ?`, events.TaskCreated, tk.ID.String()).Scan(&rows).Error)
	require.Len(t, rows, 1)
	env, err := json.Marshal(events.Envelope{EventID: rows[0].ID, Type: rows[0].Type, Payload: json.RawMessage(rows[0].Payload)})
	require.NoError(t, err)
	require.NoError(t, b.d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+rows[0].Type, env)))
	for _, m := range b.messages(t) {
		if m.Kind == telegram.KindCard && m.Ref == tk.ID.String() {
			return true
		}
	}
	return false
}

// Приёмка §8.5: «/new TM Настроить экспорт» в группе с двумя проектами — задача в проекте TM:
// тип task, приоритет medium, в бэклоге, автор — отправитель; в группу — карточка §8.3.
func TestNewCommandInGroup(t *testing.T) {
	b := newNewBot(t)
	other := b.second(t)
	chat := -100_000_000 - testutil.Next()
	b.bindGroup(t, chat, b.pr, other)

	require.Empty(t, b.sayIn(t, b.member, chat, models.ChatTypeSupergroup, "/new "+b.pr.Key+" Настроить экспорт"),
		"ответ — карточка задачи из события, отдельного ответа нет")
	tk := b.last(t, b.pr)
	require.Equal(t, "Настроить экспорт", tk.Title)
	require.Equal(t, "task", tk.Type)
	require.Equal(t, "medium", tk.Priority)
	require.Equal(t, task.StatusTodo, tk.Status)
	require.Nil(t, tk.SprintID)
	require.Nil(t, tk.AssigneeID)
	require.Equal(t, b.member.ID, tk.ReporterID)
	require.True(t, b.cardQueued(t, tk))

	// Ключ без учёта регистра; первое слово, не совпадающее с проектом группы, — часть названия.
	b.sayIn(t, b.member, chat, models.ChatTypeSupergroup, "/new "+other.Key[:1]+toLower(other.Key[1:])+" Вторая задача")
	require.Equal(t, "Вторая задача", b.last(t, other).Title)

	// Без ключа в группе с двумя проектами — подсказка со списком.
	reply := b.sayIn(t, b.member, chat, models.ChatTypeSupergroup, "/new Без ключа")
	require.Len(t, reply, 1)
	require.Contains(t, reply[0], "К группе подключено несколько проектов")
	require.Contains(t, reply[0], b.pr.Key)
	require.Contains(t, reply[0], other.Key)
}

func toLower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}

// §8.5: в группе с одним проектом ключ можно опускать; первое слово — ключ только при
// совпадении с проектом группы.
func TestNewCommandSingleProjectGroup(t *testing.T) {
	b := newNewBot(t)
	chat := -100_000_000 - testutil.Next()
	b.bindGroup(t, chat, b.pr)

	b.sayIn(t, b.member, chat, models.ChatTypeGroup, "/new XYZ отчёт")
	require.Equal(t, "XYZ отчёт", b.last(t, b.pr).Title)

	// Событие «Создана задача» выключено — карточки не будет, бот отвечает текстом.
	require.NoError(t, b.g.Exec(`UPDATE projects SET notification_settings = '{"task_created": false}' WHERE id = ?`, b.pr.ID).Error)
	reply := b.sayIn(t, b.member, chat, models.ChatTypeGroup, "/new Без карточки")
	require.Equal(t, []string{"Создана задача " + b.last(t, b.pr).Key + " «Без карточки»"}, reply)

	// Группа без проектов, пустая команда, ключ без названия.
	require.Equal(t, []string{"К этой группе не подключён ни один проект"},
		b.sayIn(t, b.member, -100_000_000-testutil.Next(), models.ChatTypeGroup, "/new Задача"))
	require.Equal(t, []string{"Формат: /new [ключ проекта] <название>"}, b.sayIn(t, b.member, chat, models.ChatTypeGroup, "/new"))
	require.Equal(t, []string{"Формат: /new [ключ проекта] <название>"}, b.sayIn(t, b.member, chat, models.ChatTypeGroup, "/new "+b.pr.Key))

	// Отправитель не участник проекта группы.
	outsider := b.linked(t, "Посторонний")
	require.Equal(t, []string{"Вы не участник проекта " + b.pr.Key}, b.sayIn(t, outsider, chat, models.ChatTypeGroup, "/new Чужая"))
}

// §8.5: в личном чате — «Создана задача TM-43 «…»» с кнопкой «Открыть»; ключ проекта нужен,
// если у пользователя больше одного проекта.
func TestNewCommandPrivate(t *testing.T) {
	b := newNewBot(t)

	// Один проект — ключ можно не писать.
	upd := msgUpdate(b.tg[b.member.ID], models.ChatTypePrivate, "/new Первая")
	upd.Message.From.ID = b.tg[b.member.ID]
	b.p.Handle(context.Background(), upd, time.Now())
	msgs := b.messages(t)
	require.Len(t, msgs, 1)
	tk := b.last(t, b.pr)
	require.Equal(t, "Создана задача "+tk.Key+" «Первая»", msgs[0].Text)
	require.NotNil(t, msgs[0].Keyboard)
	require.Equal(t, "Открыть", msgs[0].Keyboard.InlineKeyboard[0][0].Text)
	require.Equal(t, "https://tasks.example/tasks/"+tk.Key, msgs[0].Keyboard.InlineKeyboard[0][0].URL)
	require.Equal(t, b.member.ID, tk.ReporterID)

	other := b.second(t)
	reply := b.say(t, b.member, "/new Без ключа")
	require.Len(t, reply, 1)
	require.Contains(t, reply[0], "Укажите проект")
	require.Contains(t, reply[0], other.Key)

	require.Equal(t, []string{"Создана задача " + other.Key + "-1 «С ключом»"}, b.say(t, b.member, "/new "+toLower(other.Key)+" С ключом"))

	// Чужой проект не считается ключом: слово уходит в название — и проект надо выбрать.
	foreign, err := b.projects.Create(context.Background(), project.CreateInput{Key: testutil.UniqueKey(t), Name: "Чужой"})
	require.NoError(t, err)
	reply = b.say(t, b.member, "/new "+foreign.Key+" задача")
	require.Contains(t, reply[0], "Укажите проект")
}

// §8.5, §3.2: /new в архивном проекте — «Проект TM в архиве, изменения недоступны».
func TestNewCommandArchived(t *testing.T) {
	b := newNewBot(t)
	require.NoError(t, b.g.Exec(`UPDATE projects SET is_archived = true WHERE id = ?`, b.pr.ID).Error)
	require.Equal(t, []string{"Проект " + b.pr.Key + " в архиве, изменения недоступны"}, b.say(t, b.member, "/new "+b.pr.Key+" Задача"))
	var n int64
	require.NoError(t, b.g.Model(&task.Task{}).Where("project_id = ?", b.pr.ID).Count(&n).Error)
	require.Zero(t, n)

	// Ни одного активного проекта и ключа нет.
	require.Equal(t, []string{"У вас нет активных проектов"}, b.say(t, b.member, "/new Задача"))
}
