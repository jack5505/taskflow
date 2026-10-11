package telegram_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// personalBot — проект с менеджером (автор задач) и исполнителем, оба привязаны к Telegram;
// диспетчер событий с личными уведомлениями и роутер с кнопками «Принять» и «Вернуть».
type personalBot struct {
	*env
	g         *gorm.DB
	projects  *project.Service
	tasks     *task.Service
	d         *events.Dispatcher
	p         *telegram.Processor
	pr        *project.Project
	admin     *user.User
	manager   *user.User
	member    *user.User
	tg        map[uuid.UUID]int64
	processed map[string]bool
}

func newPersonalBot(t *testing.T) *personalBot {
	t.Helper()
	e := newEnv(t)
	g := testutil.SharedDB(t)
	b := &personalBot{env: e, g: g, projects: project.NewService(g), tg: map[uuid.UUID]int64{}, processed: map[string]bool{}}
	b.tasks = task.NewService(g, b.projects)
	b.d = events.NewDispatcher(nil)
	rt := telegram.NewRouter("TaskBot")
	telegram.RegisterAccount(rt, telegram.NewAccounts(g, "TaskBot"), e.rdb)
	telegram.RegisterPersonal(rt, b.d, g, b.tasks, e.rdb, e.jobs, "https://tasks.example/")
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
	b.dispatch(t)
	b.messages(t)
	return b
}

// linked — новый пользователь с привязанным Telegram.
func (b *personalBot) linked(t *testing.T, name string) *user.User {
	t.Helper()
	u := newUser(t, b.g, name)
	id := tgID()
	require.NoError(t, b.g.Model(u).Update("telegram_user_id", id).Error)
	b.tg[u.ID] = id
	return u
}

func (b *personalBot) join(t *testing.T, u *user.User, role string) {
	t.Helper()
	a, err := b.projects.Access(context.Background(), b.admin, b.pr.Key)
	require.NoError(t, err)
	_, _, err = b.projects.PutMember(audit.WithActor(context.Background(), b.admin.ID), a, u.ID, role)
	require.NoError(t, err)
}

// create — задача менеджера с исполнителем assignee (nil — без исполнителя).
func (b *personalBot) create(t *testing.T, assignee *uuid.UUID) *task.Task {
	t.Helper()
	a, err := b.projects.Access(context.Background(), b.manager, b.pr.Key)
	require.NoError(t, err)
	tk, err := b.tasks.Create(context.Background(), a, b.manager, task.CreateInput{Title: "Экспорт <CSV>", AssigneeID: assignee})
	require.NoError(t, err)
	return tk
}

func (b *personalBot) move(t *testing.T, u *user.User, key, to, comment string) {
	t.Helper()
	_, err := b.tasks.Transition(context.Background(), u, key, task.TransitionInput{To: to, Comment: comment})
	require.NoError(t, err)
}

// dispatch прогоняет через диспетчер новые события outbox этого проекта, как воркер.
func (b *personalBot) dispatch(t *testing.T) {
	t.Helper()
	var rows []struct {
		ID      string
		Type    string
		Payload string
	}
	require.NoError(t, b.g.Raw(`SELECT id::text AS id, type, payload::text AS payload FROM outbox_events
		WHERE payload->>'project_id' = ? ORDER BY created_at, id`, b.pr.ID.String()).Scan(&rows).Error)
	for _, r := range rows {
		if b.processed[r.ID] {
			continue
		}
		b.processed[r.ID] = true
		raw, err := json.Marshal(events.Envelope{EventID: r.ID, Type: r.Type, Payload: []byte(r.Payload)})
		require.NoError(t, err)
		require.NoError(t, b.d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+r.Type, raw)))
	}
}

// inbox — сообщения из очереди, сгруппированные по чату.
func (b *personalBot) inbox(t *testing.T) map[int64][]telegram.Message {
	t.Helper()
	out := map[int64][]telegram.Message{}
	for _, m := range b.messages(t) {
		out[m.ChatID] = append(out[m.ChatID], m)
	}
	return out
}

// buttons — подписи кнопок и их callback_data или url.
func buttons(m telegram.Message) []string {
	if m.Keyboard == nil {
		return nil
	}
	var out []string
	for _, row := range m.Keyboard.InlineKeyboard {
		for _, k := range row {
			out = append(out, k.Text+"|"+k.CallbackData+k.URL)
		}
	}
	return out
}

// press — нажатие кнопки data под сообщением messageID с текстом text в личном чате u.
func (b *personalBot) press(u *user.User, data string, messageID int, text string) {
	tg := b.tg[u.ID]
	upd := &models.Update{ID: testutil.Next(), CallbackQuery: &models.CallbackQuery{ID: "cb", Data: data, From: models.User{ID: tg},
		Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: messageID, Text: text, Chat: models.Chat{ID: tg, Type: models.ChatTypePrivate}}}}}
	b.p.Handle(context.Background(), upd, time.Now())
}

// say — обычный текст от u в личный чат.
func (b *personalBot) say(u *user.User, text string) {
	tg := b.tg[u.ID]
	upd := msgUpdate(tg, models.ChatTypePrivate, text)
	upd.Message.From.ID = tg
	b.p.Handle(context.Background(), upd, time.Now())
}

// alert — текст последнего answerCallbackQuery.
func (b *personalBot) alert(t *testing.T) string {
	t.Helper()
	calls := b.env.tg.Calls("answerCallbackQuery")
	require.NotEmpty(t, calls)
	return calls[len(calls)-1].Params["text"]
}

func (b *personalBot) status(t *testing.T, key string) string {
	t.Helper()
	var s string
	require.NoError(t, b.g.Raw(`SELECT status FROM tasks WHERE key = ?`, key).Scan(&s).Error)
	return s
}

// Сценарий приёмки §10.4: автор получает «отправлена на проверку» с кнопками; «Принять»
// переводит задачу в done; повторное нажатие ничего не делает.
func TestReviewNotificationAccept(t *testing.T) {
	b := newPersonalBot(t)
	tk := b.create(t, &b.member.ID)
	b.dispatch(t)
	b.messages(t) // назначение
	b.move(t, b.member, tk.Key, task.StatusInProgress, "")
	b.move(t, b.member, tk.Key, task.StatusReview, "")
	b.dispatch(t)

	inbox := b.inbox(t)
	require.Len(t, inbox, 1, "только автору: исполнитель сам отправил на проверку")
	msg := inbox[b.tg[b.manager.ID]]
	require.Len(t, msg, 1)
	require.Equal(t, "🔍 "+tk.Key+" «Экспорт <CSV>» отправлена на проверку — Иван Петров", msg[0].Text)
	require.False(t, msg[0].HTML)
	require.Equal(t, []string{"Принять|accept:" + tk.ID.String(), "Вернуть|rework:" + tk.ID.String(),
		"Открыть|https://tasks.example/tasks/" + tk.Key}, buttons(msg[0]))

	b.press(b.manager, "accept:"+tk.ID.String(), 55, msg[0].Text)
	require.Equal(t, task.StatusDone, b.status(t, tk.Key))
	require.Equal(t, tk.Key+" принята", b.alert(t))
	edits := b.messages(t)
	require.Len(t, edits, 1, "кнопки убраны из уведомления")
	require.Equal(t, telegram.OpEdit, edits[0].Op)
	require.Equal(t, 55, edits[0].MessageID)
	require.Equal(t, msg[0].Text+"\n\n✅ Принята", edits[0].Text)
	require.Equal(t, []string{"Открыть|https://tasks.example/tasks/" + tk.Key}, buttons(edits[0]))

	// Исполнитель узнаёт о приёмке.
	b.dispatch(t)
	inbox = b.inbox(t)
	require.Equal(t, "✅ "+tk.Key+" «Экспорт <CSV>» принята — Анна Смирнова", inbox[b.tg[b.member.ID]][0].Text)
	require.Empty(t, inbox[b.tg[b.manager.ID]], "о своём действии — нет")

	// §8.8: повторное нажатие не создаёт повторных действий.
	b.press(b.manager, "accept:"+tk.ID.String(), 55, msg[0].Text)
	require.Equal(t, "Задача "+tk.Key+" уже не на проверке, статус: Готово", b.alert(t))
	require.Empty(t, b.messages(t))
}

// §8.7: «Вернуть» запрашивает комментарий следующим сообщением и возвращает задачу в in_progress.
func TestReviewNotificationRework(t *testing.T) {
	b := newPersonalBot(t)
	tk := b.create(t, &b.member.ID)
	b.move(t, b.member, tk.Key, task.StatusInProgress, "")
	b.move(t, b.member, tk.Key, task.StatusReview, "")
	b.dispatch(t)
	b.messages(t)

	// Текст без нажатия «Вернуть» бот игнорирует; непривязанному на текст тоже не отвечает
	// (§8.1 — ответ о привязке только на команды и кнопки), текст в группе не читает.
	b.say(b.manager, "просто текст")
	stranger := msgUpdate(tgID(), models.ChatTypePrivate, "привет")
	b.p.Handle(context.Background(), stranger, time.Now())
	b.p.Handle(context.Background(), msgUpdate(-100, models.ChatTypeGroup, "текст в группе"), time.Now())
	require.Empty(t, b.messages(t))

	b.press(b.manager, "rework:"+tk.ID.String(), 56, "🔍 уведомление")
	require.Equal(t, []string{"Напишите следующим сообщением причину возврата " + tk.Key + " на доработку (не короче 5 символов)"},
		texts(b.messages(t)))
	require.Equal(t, task.StatusReview, b.status(t, tk.Key), "без причины задача не меняется")

	b.say(b.manager, "мало")
	require.Equal(t, []string{"Причина слишком короткая: нужно не меньше 5 символов. Напишите её ещё раз"}, texts(b.messages(t)))

	b.say(b.manager, "Нет итогов по месяцу")
	require.Equal(t, task.StatusInProgress, b.status(t, tk.Key))
	msgs := b.messages(t)
	require.Len(t, msgs, 2)
	require.Equal(t, telegram.OpEdit, msgs[0].Op)
	require.Equal(t, "🔍 уведомление\n\n🔁 Возвращена на доработку", msgs[0].Text)
	require.Equal(t, tk.Key+" возвращена на доработку", msgs[1].Text)
	var comment string
	require.NoError(t, b.g.Raw(`SELECT body FROM comments WHERE task_id = ?`, tk.ID).Scan(&comment).Error)
	require.Equal(t, "Нет итогов по месяцу", comment)

	// Исполнитель получает доработку с комментарием.
	b.dispatch(t)
	inbox := b.inbox(t)
	require.Equal(t, "🔁 "+tk.Key+" «Экспорт <CSV>» возвращена на доработку — Анна Смирнова: Нет итогов по месяцу",
		inbox[b.tg[b.member.ID]][0].Text)

	// Состояние израсходовано: следующий текст снова игнорируется.
	b.say(b.manager, "ещё одна причина")
	require.Empty(t, b.messages(t))

	// «Вернуть» на задаче не на проверке — сразу отказ.
	b.press(b.manager, "rework:"+tk.ID.String(), 56, "🔍 уведомление")
	require.Equal(t, "Задача "+tk.Key+" уже не на проверке, статус: В работе", b.alert(t))
}

// §8.7: автор и исполнитель — одно лицо: кнопки «Принять» нет (принимает менеджер, §4).
// Права проверяются при нажатии: исполнитель не может «Вернуть» чужую задачу.
func TestReviewOwnTaskHasNoAccept(t *testing.T) {
	b := newPersonalBot(t)
	a, err := b.projects.Access(context.Background(), b.member, b.pr.Key)
	require.NoError(t, err)
	own, err := b.tasks.Create(context.Background(), a, b.member, task.CreateInput{Title: "Своя"})
	require.NoError(t, err)
	b.move(t, b.member, own.Key, task.StatusInProgress, "")
	b.move(t, b.manager, own.Key, task.StatusReview, "") // на проверку отправил менеджер
	b.dispatch(t)
	msg := b.inbox(t)[b.tg[b.member.ID]]
	require.Len(t, msg, 1)
	require.Equal(t, []string{"Вернуть|rework:" + own.ID.String(), "Открыть|https://tasks.example/tasks/" + own.Key}, buttons(msg[0]))

	// Подделанная кнопка «Принять» — отказ сервиса §4.
	b.press(b.member, "accept:"+own.ID.String(), 1, "x")
	require.Equal(t, "Свою задачу, где вы и автор, и исполнитель, принимает менеджер", b.alert(t))
	require.Equal(t, task.StatusReview, b.status(t, own.Key))

	// Чужая задача: исполнитель не автор и не менеджер — «Вернуть» недоступно.
	tk := b.create(t, &b.member.ID)
	b.move(t, b.member, tk.Key, task.StatusInProgress, "")
	b.move(t, b.member, tk.Key, task.StatusReview, "")
	b.press(b.member, "rework:"+tk.ID.String(), 2, "x")
	require.Equal(t, "Вернуть на доработку может автор задачи или менеджер проекта", b.alert(t))
	b.say(b.member, "Причина возврата")
	require.Empty(t, b.messages(t), "причину не ждём")
	require.Equal(t, task.StatusReview, b.status(t, tk.Key))

	// Чужой проект: ключ задачи не раскрывается.
	outsider := b.linked(t, "Посторонний")
	b.press(outsider, "rework:"+tk.ID.String(), 2, "x")
	require.Equal(t, "Задача не найдена", b.alert(t))
	b.press(outsider, "accept:"+tk.ID.String(), 2, "x")
	require.Equal(t, "Задача не найдена", b.alert(t))

	// Неизвестная задача.
	b.messages(t)
	b.press(b.manager, "accept:"+uuid.NewString(), 3, "x")
	require.Equal(t, "Задача не найдена", b.alert(t))
	b.press(b.manager, "rework:bad", 3, "x")
	require.Equal(t, "Задача не найдена", b.alert(t))
}

// §8.7: каждое уведомление можно отключить в профиле; без привязки уведомлений нет.
func TestPersonalRespectsSettingsAndLink(t *testing.T) {
	b := newPersonalBot(t)
	settings, err := user.MergeNotifications(nil, map[string]bool{user.NotifyAssigned: false})
	require.NoError(t, err)
	require.NoError(t, b.g.Model(b.member).Update("notification_settings", settings).Error)
	b.create(t, &b.member.ID)
	b.dispatch(t)
	require.Empty(t, b.messages(t), "назначение выключено")

	stranger := newUser(t, b.g, "Без Telegram")
	b.join(t, stranger, project.RoleMember)
	b.create(t, &stranger.ID)
	b.dispatch(t)
	require.Empty(t, b.messages(t), "Telegram не привязан")
}

// §8.7: назначение с кнопками «Взять в работу» и «Открыть», комментарий, удаление, отмена,
// участие в проекте.
func TestPersonalEvents(t *testing.T) {
	b := newPersonalBot(t)
	ctx := context.Background()
	tk := b.create(t, &b.member.ID)
	b.dispatch(t)
	msg := b.inbox(t)[b.tg[b.member.ID]]
	require.Len(t, msg, 1)
	require.Equal(t, "📌 Вас назначили исполнителем "+tk.Key+" «Экспорт <CSV>» — Анна Смирнова", msg[0].Text)
	require.Equal(t, []string{"Взять в работу|take:" + tk.ID.String(), "Открыть|https://tasks.example/tasks/" + tk.Key}, buttons(msg[0]))

	// Комментарий: исполнителю и автору, кроме автора комментария.
	_, err := b.tasks.AddComment(ctx, b.member, tk.Key, "Начну завтра")
	require.NoError(t, err)
	b.dispatch(t)
	inbox := b.inbox(t)
	require.Len(t, inbox, 1)
	require.Equal(t, "💬 Иван Петров к "+tk.Key+" «Экспорт <CSV>»: Начну завтра", inbox[b.tg[b.manager.ID]][0].Text)

	// Отмена менеджером: исполнителю, с причиной.
	b.move(t, b.manager, tk.Key, task.StatusCancelled, "Не нужна")
	b.dispatch(t)
	inbox = b.inbox(t)
	require.Len(t, inbox, 1)
	require.Equal(t, "❌ "+tk.Key+" «Экспорт <CSV>» отменена — Анна Смирнова: Не нужна", inbox[b.tg[b.member.ID]][0].Text)

	// Переходы без личного уведомления: взятие в работу.
	tk2 := b.create(t, nil)
	b.dispatch(t)
	b.messages(t)
	b.move(t, b.member, tk2.Key, task.StatusInProgress, "")
	b.dispatch(t)
	require.Empty(t, b.messages(t))

	// Удаление: исполнителю и автору, кроме удалившего; без кнопки «Открыть».
	require.NoError(t, b.tasks.Delete(ctx, b.manager, tk2.Key))
	b.dispatch(t)
	inbox = b.inbox(t)
	require.Len(t, inbox, 1)
	require.Equal(t, "🗑 "+tk2.Key+" «Экспорт <CSV>» удалена — Анна Смирнова", inbox[b.tg[b.member.ID]][0].Text)
	require.Nil(t, inbox[b.tg[b.member.ID]][0].Keyboard)

	// Участие в проекте: смена роли и исключение.
	b.join(t, b.member, project.RoleManager)
	a, err := b.projects.Access(ctx, b.admin, b.pr.Key)
	require.NoError(t, err)
	require.NoError(t, b.projects.RemoveMember(audit.WithActor(ctx, b.admin.ID), a, b.member.ID))
	newcomer := b.linked(t, "Новичок")
	b.join(t, newcomer, project.RoleMember)
	b.dispatch(t)
	inbox = b.inbox(t)
	require.Equal(t, []string{
		"👥 Ваша роль в проекте " + b.pr.Key + " «Тайм-менеджмент» изменена: участник → менеджер — Админ",
		"👥 Вас исключили из проекта " + b.pr.Key + " «Тайм-менеджмент» — Админ",
	}, texts(inbox[b.tg[b.member.ID]]))
	require.Equal(t, []string{"👥 Вас добавили в проект " + b.pr.Key + " «Тайм-менеджмент», роль: участник — Админ"},
		texts(inbox[b.tg[newcomer.ID]]))
}

// §8.7: автоостановка таймера и обрезка записи по дневному лимиту — владельцу таймера.
func TestPersonalTimerEvents(t *testing.T) {
	b := newPersonalBot(t)
	tk := b.create(t, &b.member.ID)
	b.dispatch(t)
	b.messages(t)
	send := func(typ string, payload map[string]any) {
		payload["user_id"], payload["task_id"], payload["key"], payload["title"], payload["project_id"] =
			b.member.ID, tk.ID, tk.Key, tk.Title, b.pr.ID
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		env, err := json.Marshal(events.Envelope{EventID: uuid.NewString(), Type: typ, Payload: raw})
		require.NoError(t, err)
		require.NoError(t, b.d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+typ, env)))
	}
	send(events.TimerAutoStopped, map[string]any{"minutes": 720})
	send(events.TimerTrimmed, map[string]any{"work_date": "2026-10-11", "requested_minutes": 150, "written_minutes": 30})
	require.Equal(t, []string{
		"⏹ Таймер на " + tk.Key + " «Экспорт <CSV>» остановлен автоматически через 12 часов, списано 12h",
		"✂️ Запись таймера на " + tk.Key + " «Экспорт <CSV>» за 11.10.2026 обрезана по дневному лимиту 24 часа: списано 30m из 2h 30m",
	}, texts(b.inbox(t)[b.tg[b.member.ID]]))

	// Битый payload — ошибка обработчика (asynq повторит).
	env, err := json.Marshal(events.Envelope{EventID: uuid.NewString(), Type: events.TimerTrimmed, Payload: []byte(`[]`)})
	require.NoError(t, err)
	require.Error(t, b.d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+events.TimerTrimmed, env)))
}

func texts(ms []telegram.Message) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, strings.TrimSpace(m.Text))
	}
	return out
}

// Ожидание причины не переживает смену статуса: принятую задачу текст не переоткрывает.
func TestReworkReasonAfterAccept(t *testing.T) {
	b := newPersonalBot(t)
	review := func() *task.Task {
		tk := b.create(t, &b.member.ID)
		b.move(t, b.member, tk.Key, task.StatusInProgress, "")
		b.move(t, b.member, tk.Key, task.StatusReview, "")
		return tk
	}
	// Задачу приняли в вебе, пока бот ждал причину.
	tk := review()
	b.press(b.manager, "rework:"+tk.ID.String(), 1, "x")
	b.move(t, b.manager, tk.Key, task.StatusDone, "")
	b.messages(t)
	b.say(b.manager, "Всё-таки вернуть")
	require.Equal(t, []string{"Задача " + tk.Key + " уже не на проверке, статус: Готово"}, texts(b.messages(t)))
	require.Equal(t, task.StatusDone, b.status(t, tk.Key))

	// «Вернуть», затем «Принять» той же задачи: ожидание снимается.
	tk = review()
	b.press(b.manager, "rework:"+tk.ID.String(), 1, "x")
	b.press(b.manager, "accept:"+tk.ID.String(), 1, "x")
	require.Equal(t, task.StatusDone, b.status(t, tk.Key))
	b.messages(t)
	b.say(b.manager, "спасибо, всё ок")
	require.Empty(t, b.messages(t))
}

// Назначение на задачу, удалённую до обработки события, не приходит.
func TestAssignedDeletedTask(t *testing.T) {
	b := newPersonalBot(t)
	tk := b.create(t, &b.member.ID)
	require.NoError(t, b.tasks.Delete(context.Background(), b.manager, tk.Key))
	b.dispatch(t)
	inbox := b.inbox(t)
	require.Len(t, inbox[b.tg[b.member.ID]], 1)
	require.True(t, strings.HasPrefix(inbox[b.tg[b.member.ID]][0].Text, "🗑"), "только об удалении")
}
