package telegram_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/user"
)

// cardBot — группа с подключённым проектом, сервис задач, обработчики событий и отправитель,
// как в воркере.
type cardBot struct {
	*groupBot
	tasks   *task.Service
	d       *events.Dispatcher
	send    *telegram.Deliverer
	seen    map[string]bool
	manager *user.User
	member  *user.User
	p       *project.Project
	chatID  int64
}

func newCardBot(t *testing.T) *cardBot {
	gb := newGroupBot(t)
	b := &cardBot{groupBot: gb, tasks: task.NewService(gb.g, project.NewService(gb.g)), d: events.NewDispatcher(nil), seen: map[string]bool{}}
	rt := telegram.NewRouter("TaskBot")
	telegram.RegisterCards(rt, b.d, telegram.NewCards(gb.g, gb.out, "https://tasks.example/"))
	b.send = telegram.NewDeliverer(gb.api, gb.rdb, gb.out).WithRouter(rt)

	var mgrTG int64
	b.manager, mgrTG = gb.linked(t, "Анна Смирнова")
	b.member, _ = gb.linked(t, "Иван Петров")
	require.NoError(t, gb.g.Model(b.member).Update("telegram_username", "ivan").Error)
	b.p = gb.project(t, "Тайм-менеджмент", map[*user.User]string{b.manager: project.RoleManager, b.member: project.RoleMember})
	chat := group("Команда")
	b.chatID = chat.ID
	gb.say(mgrTG, chat, "/bind "+gb.code(t, b.manager, b.p))
	gb.messages(t)
	return b
}

// events передаёт обработчикам новые события из outbox, как релей воркера.
func (b *cardBot) events(t *testing.T) {
	t.Helper()
	var rows []struct {
		ID      string
		Type    string
		Payload string
	}
	require.NoError(t, b.g.Raw(`SELECT id, type, payload::text AS payload FROM outbox_events
		WHERE payload->>'project_id' = ? OR type = 'user.deactivated' ORDER BY created_at`, b.p.ID.String()).Scan(&rows).Error)
	for _, r := range rows {
		if b.seen[r.ID] {
			continue
		}
		b.seen[r.ID] = true
		env, err := json.Marshal(events.Envelope{EventID: r.ID, Type: r.Type, Payload: json.RawMessage(r.Payload)})
		require.NoError(t, err)
		require.NoError(t, b.d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+r.Type, env)))
	}
}

// flush доставляет все задания telegram:send (и поставленные ими) через отправитель.
func (b *cardBot) flush(t *testing.T) int {
	t.Helper()
	n := 0
	for range 10 {
		msgs := b.messages(t)
		if len(msgs) == 0 {
			return n
		}
		for _, m := range msgs {
			n++
			require.NoError(t, b.deliver(t, b.send, m))
		}
	}
	return n
}

func (b *cardBot) create(t *testing.T, in task.CreateInput) *task.Task {
	t.Helper()
	tk, err := b.tasks.Create(context.Background(), b.access(t, b.manager, b.p), b.manager, in)
	require.NoError(t, err)
	return tk
}

func (b *cardBot) messageID(t *testing.T, tk *task.Task) *int64 { return messageID(t, b.g, tk.ID) }

// Приёмка §10.4 «при создании задачи в группе появляется сообщение с кнопкой «Взять в работу»
// не позже 5 с»: событие сразу ставит отправку без задержки; формат §8.3, message_id сохранён.
func TestCardPublishedOnCreate(t *testing.T) {
	b := newCardBot(t)
	est, due := 180, "2026-10-14"
	start := time.Now()
	tk := b.create(t, task.CreateInput{Title: "Настроить экспорт <отчёта> в CSV", Priority: "high", EstimateMinutes: &est, DueDate: &due})
	b.events(t)
	queued := b.queued(t, telegram.TaskSend)
	require.Len(t, queued, 1)
	require.Equal(t, "pending", queued[0].State.String(), "карточка уходит сразу, без задержки")
	require.Equal(t, 1, b.flush(t))
	require.Less(t, time.Since(start), 5*time.Second)

	calls := b.tg.Calls("sendMessage")
	require.Len(t, calls, 1)
	c := calls[0].Params
	require.Equal(t, itoa64(b.chatID), c["chat_id"])
	require.Equal(t, "HTML", c["parse_mode"])
	require.Equal(t, "🆕 Новая задача "+tk.Key+" · Задача · Приоритет: Высокий\n"+
		"<b>Настроить экспорт &lt;отчёта&gt; в CSV</b>\n\n"+
		"Автор: Анна Смирнова\n"+
		"Оценка: 3h · Срок: 14.10.2026\n"+
		"Статус: К выполнению · Исполнитель: —", c["text"])
	require.JSONEq(t, `{"inline_keyboard":[[{"text":"Взять в работу","callback_data":"take:`+tk.ID.String()+`"},
		{"text":"Открыть","url":"https://tasks.example/tasks/`+tk.Key+`"}]]}`, c["reply_markup"])
	require.NotNil(t, b.messageID(t, tk))
	require.EqualValues(t, 101, *b.messageID(t, tk))
}

// §8.3: при смене статуса и исполнителя карточка редактируется, а не публикуется заново; события
// одной операции (взятие — назначение и переход) дают одну правку; «Взять в работу» убирается.
func TestCardEditedOnChange(t *testing.T) {
	b := newCardBot(t)
	tk := b.create(t, task.CreateInput{Title: "Задача"})
	b.events(t)
	b.flush(t)

	_, err := b.tasks.Take(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	b.events(t)
	queued := b.queued(t, telegram.TaskSend)
	require.Len(t, queued, 1, "события взятия объединены в одну правку")
	require.Equal(t, "scheduled", queued[0].State.String())
	require.Equal(t, 1, b.flush(t))

	require.Len(t, b.tg.Calls("sendMessage"), 1)
	edits := b.tg.Calls("editMessageText")
	require.Len(t, edits, 1)
	e := edits[0].Params
	require.Equal(t, "101", e["message_id"])
	require.Contains(t, e["text"], "Статус: В работе · Исполнитель: Иван Петров @ivan")
	require.JSONEq(t, `{"inline_keyboard":[[{"text":"Открыть","url":"https://tasks.example/tasks/`+tk.Key+`"}]]}`, e["reply_markup"])

	// Правка описания карточку не трогает.
	desc := "описание"
	_, err = b.tasks.Update(context.Background(), b.manager, tk.Key, nil, task.UpdateInput{Description: task.Opt[string]{Set: true, Value: &desc}})
	require.NoError(t, err)
	b.events(t)
	require.Empty(t, b.queued(t, telegram.TaskSend))
}

// §8.3: если Telegram не даёт отредактировать сообщение (старше 48 часов), бот публикует новое
// и обновляет telegram_message_id.
func TestCardReplacedWhenEditRefused(t *testing.T) {
	b := newCardBot(t)
	tk := b.create(t, task.CreateInput{Title: "Задача"})
	b.events(t)
	b.flush(t)
	old := *b.messageID(t, tk)

	b.tg.Respond("editMessageText", `{"ok":false,"error_code":400,"description":"Bad Request: message can't be edited"}`)
	_, err := b.tasks.Transition(context.Background(), b.manager, tk.Key, task.TransitionInput{To: "cancelled", Comment: "не нужна"})
	require.NoError(t, err)
	b.events(t)
	require.Equal(t, 2, b.flush(t), "правка, затем новое сообщение")
	sends := b.tg.Calls("sendMessage")
	require.Len(t, sends, 2)
	require.Contains(t, sends[1].Params["text"], "Статус: Отменена")
	require.NotEqual(t, old, *b.messageID(t, tk))
}

// §4: при удалении задачи сообщение редактируется на «Задача удалена» без кнопок.
func TestCardOnDelete(t *testing.T) {
	b := newCardBot(t)
	tk := b.create(t, task.CreateInput{Title: "Удаляемая"})
	b.events(t)
	b.flush(t)

	require.NoError(t, b.tasks.Delete(context.Background(), b.manager, tk.Key))
	b.events(t)
	b.flush(t)
	edits := b.tg.Calls("editMessageText")
	require.Len(t, edits, 1)
	require.Equal(t, "🗑 Задача удалена: "+tk.Key+" «Удаляемая»", edits[0].Params["text"])
	require.Empty(t, edits[0].Params["reply_markup"])
}

// Карточки нет, если группа не подключена или событие «Создана задача» выключено (§8.6);
// без карточки правки не отправляются.
func TestCardSkipped(t *testing.T) {
	b := newCardBot(t)
	require.NoError(t, b.g.Model(b.p).Update("notification_settings", `{"task_created":false}`).Error)
	tk := b.create(t, task.CreateInput{Title: "Без карточки"})
	b.events(t)
	b.flush(t)
	_, err := b.tasks.Take(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	b.events(t)
	b.flush(t)
	require.Empty(t, b.tg.Calls(""))
	require.Nil(t, b.messageID(t, tk))

	require.NoError(t, b.g.Model(b.p).Updates(map[string]any{"notification_settings": `{}`, "telegram_chat_id": nil}).Error)
	b.create(t, task.CreateInput{Title: "Группа отключена"})
	b.events(t)
	b.flush(t)
	require.Empty(t, b.tg.Calls(""))
}

// Задача изменилась, пока карточка отправлялась, — после отправки ставится ещё одна правка.
func TestCardFollowsChangeDuringSend(t *testing.T) {
	b := newCardBot(t)
	tk := b.create(t, task.CreateInput{Title: "Гонка"})
	b.events(t)
	msgs := b.messages(t)
	require.Len(t, msgs, 1)

	b.tg.Delay("sendMessage", 300*time.Millisecond)
	var wg sync.WaitGroup
	wg.Go(func() { require.NoError(t, b.deliver(t, b.send, msgs[0])) })
	time.Sleep(100 * time.Millisecond) // sendMessage уже идёт
	require.NoError(t, b.g.Exec(`UPDATE tasks SET title = 'Гонка 2', version = version + 1 WHERE id = ?`, tk.ID).Error)
	wg.Wait()
	b.flush(t)
	edits := b.tg.Calls("editMessageText")
	require.Len(t, edits, 1)
	require.Contains(t, edits[0].Params["text"], "<b>Гонка 2</b>")
}

func itoa64(n int64) string { b, _ := json.Marshal(n); return string(b) }

// Деактивация освобождает задачи без событий по каждой (§3.1) — их карточки тоже правятся.
func TestCardEditedOnDeactivation(t *testing.T) {
	b := newCardBot(t)
	tk := b.create(t, task.CreateInput{Title: "Освобождаемая"})
	b.events(t)
	b.flush(t)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET assignee_id = NULL, status = 'todo', version = version + 1 WHERE id = ?`, tk.ID).Error)
	env, err := json.Marshal(events.Envelope{EventID: "11111111-1111-1111-1111-111111111111", Type: events.UserDeactivated,
		Payload: json.RawMessage(`{"released_tasks":["` + tk.Key + `"]}`)})
	require.NoError(t, err)
	require.NoError(t, b.d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+events.UserDeactivated, env)))
	require.Equal(t, 1, b.flush(t))
	require.Len(t, b.tg.Calls("editMessageText"), 1)
}

// Сообщение неизвестного вида (бот старой версии) отбрасывается без вызова Telegram.
func TestDeliverDropsUnknownKind(t *testing.T) {
	b := newCardBot(t)
	require.NoError(t, b.deliver(t, b.send, telegram.Message{Op: telegram.OpSend, Kind: "nope", Ref: "x"}))
	require.Empty(t, b.tg.Calls(""))
}
