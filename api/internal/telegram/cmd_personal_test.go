package telegram_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// cmdBot — проект с менеджером и исполнителем, оба привязаны к Telegram; роутер с привязкой
// аккаунта и личными командами /timer, /stop, /my.
type cmdBot struct {
	*env
	g        *gorm.DB
	projects *project.Service
	tasks    *task.Service
	p        *telegram.Processor
	pr       *project.Project
	admin    *user.User
	manager  *user.User
	member   *user.User
	tg       map[uuid.UUID]int64
}

func newCmdBot(t *testing.T) *cmdBot {
	t.Helper()
	e := newEnv(t)
	g := testutil.SharedDB(t)
	b := &cmdBot{env: e, g: g, projects: project.NewService(g), tg: map[uuid.UUID]int64{}}
	b.tasks = task.NewService(g, b.projects)
	rt := telegram.NewRouter("TaskBot")
	telegram.RegisterAccount(rt, telegram.NewAccounts(g, "TaskBot"), e.rdb)
	telegram.RegisterPrivateCommands(rt, b.tasks)
	b.p = telegram.NewProcessor(e.api, rt, e.out)

	b.admin = newUser(t, g, "Админ")
	require.NoError(t, g.Model(b.admin).Update("is_system_admin", true).Error)
	b.admin.IsSystemAdmin = true
	var err error
	b.pr, err = b.projects.Create(context.Background(), project.CreateInput{Key: testutil.UniqueKey(t), Name: "Тайм-менеджмент"})
	require.NoError(t, err)
	b.manager = b.linked(t, "Анна Смирнова")
	b.member = b.linked(t, "Иван Петров")
	b.join(t, b.manager, project.RoleManager)
	b.join(t, b.member, project.RoleMember)
	return b
}

func (b *cmdBot) linked(t *testing.T, name string) *user.User {
	t.Helper()
	u := newUser(t, b.g, name)
	id := tgID()
	require.NoError(t, b.g.Model(u).Update("telegram_user_id", id).Error)
	b.tg[u.ID] = id
	return u
}

func (b *cmdBot) join(t *testing.T, u *user.User, role string) {
	t.Helper()
	a, err := b.projects.Access(context.Background(), b.admin, b.pr.Key)
	require.NoError(t, err)
	_, _, err = b.projects.PutMember(audit.WithActor(context.Background(), b.admin.ID), a, u.ID, role)
	require.NoError(t, err)
}

// create — задача менеджера: исполнитель assignee (nil — без исполнителя), оценка estimate минут
// (0 — без оценки).
func (b *cmdBot) create(t *testing.T, title string, assignee *user.User, estimate int) *task.Task {
	t.Helper()
	a, err := b.projects.Access(context.Background(), b.manager, b.pr.Key)
	require.NoError(t, err)
	in := task.CreateInput{Title: title}
	if assignee != nil {
		in.AssigneeID = &assignee.ID
	}
	if estimate > 0 {
		in.EstimateMinutes = &estimate
	}
	tk, err := b.tasks.Create(context.Background(), a, b.manager, in)
	require.NoError(t, err)
	return tk
}

// working — задача исполнителя b.member в статусе in_progress.
func (b *cmdBot) working(t *testing.T, title string) *task.Task {
	t.Helper()
	tk := b.create(t, title, b.member, 0)
	_, err := b.tasks.Transition(context.Background(), b.member, tk.Key, task.TransitionInput{To: task.StatusInProgress})
	require.NoError(t, err)
	return tk
}

// say — u пишет text в личный чат с ботом; возвращает ответы бота.
func (b *cmdBot) say(t *testing.T, u *user.User, text string) []string {
	t.Helper()
	return b.sayIn(t, u, b.tg[u.ID], models.ChatTypePrivate, text)
}

func (b *cmdBot) sayIn(t *testing.T, u *user.User, chatID int64, chatType models.ChatType, text string) []string {
	t.Helper()
	upd := msgUpdate(chatID, chatType, text)
	upd.Message.From.ID = b.tg[u.ID]
	b.p.Handle(context.Background(), upd, time.Now())
	var out []string
	for _, m := range b.messages(t) {
		out = append(out, m.Text)
	}
	return out
}

// backdate сдвигает старт таймера u назад так, чтобы остановка сразу после дала ровно d
// с округлением вверх.
func (b *cmdBot) backdate(t *testing.T, u *user.User, d time.Duration) {
	t.Helper()
	require.NoError(t, b.g.Exec(`UPDATE active_timers SET started_at = started_at - ? * interval '1 second' WHERE user_id = ?`,
		int((d-30*time.Second)/time.Second), u.ID).Error)
}

func (b *cmdBot) timerTask(t *testing.T, u *user.User) string {
	t.Helper()
	var key string
	require.NoError(t, b.g.Raw(`SELECT t.key FROM active_timers a JOIN tasks t ON t.id = a.task_id WHERE a.user_id = ?`, u.ID).
		Scan(&key).Error)
	return key
}

// §8.5, §6.4: /timer <ключ> в личном чате запускает таймер; регистр ключа не важен.
func TestTimerCommandStarts(t *testing.T) {
	b := newCmdBot(t)
	tk := b.working(t, "Экспорт CSV")

	require.Equal(t, []string{"Таймер запущен на " + tk.Key}, b.say(t, b.member, "/timer "+strings.ToLower(tk.Key)))
	require.Equal(t, tk.Key, b.timerTask(t, b.member))

	// Повторный запуск на той же задаче — сообщение сервиса (409 TIMER_ALREADY_RUNNING).
	require.Equal(t, []string{"Таймер уже запущен на " + tk.Key}, b.say(t, b.member, "/timer "+tk.Key))
}

// §6.4: запуск на другой задаче останавливает текущий таймер с записью времени.
func TestTimerCommandSwitchesTask(t *testing.T) {
	b := newCmdBot(t)
	first := b.working(t, "Первая")
	second := b.working(t, "Вторая")
	b.say(t, b.member, "/timer "+first.Key)
	b.backdate(t, b.member, 47*time.Minute)

	require.Equal(t, []string{"Таймер остановлен: 47m списано на " + first.Key + "\nТаймер запущен на " + second.Key},
		b.say(t, b.member, "/timer "+second.Key))
	require.Equal(t, second.Key, b.timerTask(t, b.member))
}

// Критерий #55: /timer на чужой задаче отклоняется с понятным текстом; другие отказы сервиса —
// тоже понятным текстом, таймер не запускается.
func TestTimerCommandRejects(t *testing.T) {
	b := newCmdBot(t)
	foreign := b.working(t, "Чужая")
	todo := b.create(t, "Не начата", b.member, 0)
	outsider := b.linked(t, "Посторонний")
	missing := b.pr.Key + "-999"

	cases := []struct {
		name string
		u    *user.User
		text string
		want string
	}{
		{"чужая задача", b.manager, "/timer " + foreign.Key, "Таймер на " + foreign.Key + " может запустить только исполнитель задачи"},
		{"не в работе", b.member, "/timer " + todo.Key, "Таймер запускается только на задаче в работе, а " + todo.Key + " не в работе"},
		{"не участник проекта", outsider, "/timer " + foreign.Key, "Задача " + foreign.Key + " не найдена"},
		{"нет задачи", b.member, "/timer " + missing, "Задача " + missing + " не найдена"},
		{"без ключа", b.member, "/timer", "Формат: /timer TM-42"},
		{"без ключа проекта", b.member, "/timer 42", "Формат: /timer TM-42"},
		{"лишние слова", b.member, "/timer " + foreign.Key + " сейчас", "Формат: /timer TM-42"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, []string{c.want}, b.say(t, c.u, c.text))
			require.Empty(t, b.timerTask(t, c.u))
		})
	}
}

// §8.5: /timer, /stop и /my работают только в личном чате; в группе — подсказка, которая
// удаляется через 30 секунд.
func TestPrivateCommandsInGroup(t *testing.T) {
	b := newCmdBot(t)
	tk := b.working(t, "Экспорт")
	for _, text := range []string{"/timer " + tk.Key, "/stop", "/my"} {
		upd := msgUpdate(-100500, models.ChatTypeSupergroup, text)
		upd.Message.From.ID = b.tg[b.member.ID]
		b.p.Handle(context.Background(), upd, time.Now())
		msgs := b.messages(t)
		require.Len(t, msgs, 1, text)
		require.Equal(t, "Эта команда работает в личном чате с ботом", msgs[0].Text)
		require.Equal(t, int(telegram.TempReplyTTL.Seconds()), msgs[0].DeleteAfter)
	}
	require.Empty(t, b.timerTask(t, b.member))
}

// §8.5, §6.4: /stop останавливает таймер и пишет время в формате §6.1.
func TestStopCommand(t *testing.T) {
	b := newCmdBot(t)
	tk := b.working(t, "Экспорт")

	require.Equal(t, []string{"Таймер не запущен"}, b.say(t, b.member, "/stop"))

	b.say(t, b.member, "/timer "+tk.Key)
	b.backdate(t, b.member, 47*time.Minute)
	require.Equal(t, []string{"Таймер остановлен: 47m списано на " + tk.Key}, b.say(t, b.member, "/stop"))
	require.Empty(t, b.timerTask(t, b.member))
	var spent int
	require.NoError(t, b.g.Raw(`SELECT spent_minutes FROM tasks WHERE id = ?`, tk.ID).Scan(&spent).Error)
	require.Equal(t, 47, spent)

	// Меньше минуты — запись не создаётся (§6.4).
	b.say(t, b.member, "/timer "+tk.Key)
	require.Equal(t, []string{"Таймер на " + tk.Key + " остановлен: меньше минуты, время не списано"}, b.say(t, b.member, "/stop"))

	// Больше 12 часов — остановка на отметке 12 часов.
	b.say(t, b.member, "/timer "+tk.Key)
	b.backdate(t, b.member, 13*time.Hour)
	reply := b.say(t, b.member, "/stop")
	require.Len(t, reply, 1)
	require.Contains(t, reply[0], "Таймер шёл больше 12 часов")
}

// §8.5: /my — открытые задачи пользователя по всем проектам: ключ, название, статус,
// списано/оценка; закрытые и чужие не показываются.
func TestMyCommand(t *testing.T) {
	b := newCmdBot(t)
	require.Equal(t, []string{"Открытых задач нет"}, b.say(t, b.member, "/my"))

	todo := b.create(t, "Оценённая", b.member, 180)
	work := b.working(t, "Без оценки")
	_, err := b.tasks.AddWorklog(context.Background(), b.member, work.Key, task.WorklogInput{Duration: "1h 30m"})
	require.NoError(t, err)
	done := b.working(t, "Закрытая")
	_, err = b.tasks.Transition(context.Background(), b.manager, done.Key, task.TransitionInput{To: task.StatusCancelled, Comment: "не нужна"})
	require.NoError(t, err)
	b.create(t, "Чужая", b.manager, 0)

	reply := b.say(t, b.member, "/my")
	require.Len(t, reply, 1)
	lines := strings.Split(reply[0], "\n")
	require.Equal(t, "Ваши открытые задачи:", lines[0])
	require.ElementsMatch(t, []string{
		todo.Key + " «Оценённая» · К выполнению · 0m из 3h",
		work.Key + " «Без оценки» · В работе · 1h 30m",
	}, lines[1:])
}

// §8.5: /my показывает не больше 30 задач и говорит, сколько всего; длинные названия
// укорачиваются, чтобы список влез в одно сообщение Telegram (4096 символов).
func TestMyCommandLimit(t *testing.T) {
	b := newCmdBot(t)
	long := b.create(t, strings.Repeat("Д", 200), b.member, 0)
	for i := range telegram.MaxMyTasks + 1 {
		b.create(t, fmt.Sprintf("Задача %d %s", i, strings.Repeat("я", 190)), b.member, 0)
	}
	reply := b.say(t, b.member, "/my")
	require.Len(t, reply, 1)
	require.Less(t, len([]rune(reply[0])), 4096)
	lines := strings.Split(reply[0], "\n")
	require.Equal(t, fmt.Sprintf("Ваши открытые задачи (первые %d из %d):", telegram.MaxMyTasks, telegram.MaxMyTasks+2), lines[0])
	require.Len(t, lines, telegram.MaxMyTasks+1)
	require.Equal(t, long.Key+" «"+strings.Repeat("Д", 59)+"…» · К выполнению · 0m", lines[1])
}

// §8.1: личные команды без привязки — общий ответ.
func TestPrivateCommandsNeedLink(t *testing.T) {
	b := newCmdBot(t)
	stranger := newUser(t, b.g, "Без привязки")
	b.tg[stranger.ID] = tgID()
	require.Equal(t, []string{"Сначала привяжите аккаунт: профиль → Привязать Telegram"}, b.say(t, stranger, "/my"))
}
