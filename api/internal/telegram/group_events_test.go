package telegram_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
)

// newEventBot — cardBot, у которого есть и карточки, и сообщения о событиях (§8.6), как в воркере.
func newEventBot(t *testing.T) *cardBot {
	b := newCardBot(t)
	fixed := time.Now()
	rt := telegram.NewRouter("TaskBot")
	b.d = events.NewDispatcher(nil)
	cards := telegram.NewCards(b.g, b.out, "https://tasks.example/")
	cards.SetClock(func() time.Time { return fixed })
	telegram.RegisterCards(rt, b.d, cards)
	ge := telegram.NewGroupEvents(b.g, b.rdb, b.out)
	ge.SetClock(func() time.Time { return fixed }) // события теста — в одной секунде
	telegram.RegisterGroupEvents(rt, b.d, ge)
	b.send = telegram.NewDeliverer(b.api, b.rdb, b.out).WithRouter(rt)
	return b
}

// sent — сообщения, отправленные в группу (без карточек): текст и на что ответ.
func (b *cardBot) sent(t *testing.T) []map[string]string {
	t.Helper()
	var out []map[string]string
	for _, c := range b.tg.Calls("sendMessage") {
		if c.Params["parse_mode"] == "HTML" {
			continue // карточка задачи
		}
		reply := ""
		if rp := c.Params["reply_parameters"]; rp != "" {
			var p struct {
				MessageID int `json:"message_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(rp), &p))
			reply = itoa64(int64(p.MessageID))
		}
		out = append(out, map[string]string{"text": c.Params["text"], "reply": reply, "chat": c.Params["chat_id"]})
	}
	return out
}

func (b *cardBot) createdCard(t *testing.T, in task.CreateInput) *task.Task {
	t.Helper()
	tk := b.create(t, in)
	b.events(t)
	b.flush(t)
	return tk
}

// §8.6: взятие из веба и из бота публикуется одинаково — «🛠 … взята в работу — …» ответом на
// карточку задачи.
func TestGroupEventTaken(t *testing.T) {
	b := newEventBot(t)
	tk := b.createdCard(t, task.CreateInput{Title: "Экспорт"})
	_, err := b.tasks.Take(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	b.events(t)
	b.flush(t)

	tk2 := b.createdCard(t, task.CreateInput{Title: "Из бота"})
	_, err = b.tasks.Take(audit.WithSource(context.Background(), audit.SourceBot), b.member, tk2.Key)
	require.NoError(t, err)
	b.events(t)
	b.flush(t)

	card1, card2 := itoa64(*b.messageID(t, tk)), itoa64(*b.messageID(t, tk2))
	require.Equal(t, []map[string]string{
		{"text": "🛠 " + tk.Key + " «Экспорт» взята в работу — Иван Петров @ivan", "reply": card1, "chat": itoa64(b.chatID)},
		{"text": "🛠 " + tk2.Key + " «Из бота» взята в работу — Иван Петров @ivan", "reply": card2, "chat": itoa64(b.chatID)},
	}, b.sent(t))
}

// Приёмка #60: два перехода за секунду дают одно сообщение — по последнему состоянию.
func TestGroupEventsCoalesced(t *testing.T) {
	b := newEventBot(t)
	tk := b.createdCard(t, task.CreateInput{Title: "Быстрая"})
	_, err := b.tasks.Take(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	_, err = b.tasks.Transition(context.Background(), b.member, tk.Key, task.TransitionInput{To: "review"})
	require.NoError(t, err)
	b.events(t)
	b.flush(t)
	sent := b.sent(t)
	require.Len(t, sent, 1)
	require.Equal(t, "🔍 "+tk.Key+" «Быстрая» отправлена на проверку — Иван Петров", sent[0]["text"])
}

// Приёмка #60: отключённое в настройках проекта событие не публикуется (§8.6).
func TestGroupEventDisabled(t *testing.T) {
	b := newEventBot(t)
	require.NoError(t, b.g.Model(b.p).Update("notification_settings", `{"task_taken":false}`).Error)
	tk := b.createdCard(t, task.CreateInput{Title: "Тихая"})
	_, err := b.tasks.Take(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	b.events(t)
	b.flush(t)
	require.Empty(t, b.sent(t))
	require.Len(t, b.tg.Calls("editMessageText"), 1, "карточка правится независимо от настроек событий")

	// Включённое соседнее событие публикуется.
	_, err = b.tasks.Transition(context.Background(), b.member, tk.Key, task.TransitionInput{To: "review"})
	require.NoError(t, err)
	b.events(t)
	b.flush(t)
	require.Len(t, b.sent(t), 1)
}

// Тексты таблицы §8.6 по переходам §4 и назначению менеджером (§8.4).
func TestGroupEventTexts(t *testing.T) {
	b := newEventBot(t)
	est := 180
	tk := b.createdCard(t, task.CreateInput{Title: "Отчёт", EstimateMinutes: &est})
	step := func(fn func() error) string {
		t.Helper()
		require.NoError(t, fn())
		b.events(t)
		b.flush(t)
		sent := b.sent(t)
		require.NotEmpty(t, sent)
		return sent[len(sent)-1]["text"]
	}
	ctx := context.Background()
	member := b.member.ID
	k := tk.Key

	require.Equal(t, "📌 "+k+" «Отчёт»: исполнитель Иван Петров @ivan — назначение: Анна Смирнова", step(func() error {
		_, err := b.tasks.Update(ctx, b.manager, k, nil, task.UpdateInput{AssigneeID: task.Opt[uuid.UUID]{Set: true, Value: &member}})
		return err
	}))
	require.Equal(t, "🛠 "+k+" «Отчёт» взята в работу — Иван Петров @ivan", step(func() error {
		_, err := b.tasks.Transition(ctx, b.member, k, task.TransitionInput{To: "in_progress"})
		return err
	}))
	require.Equal(t, "↩️ "+k+" «Отчёт» возвращена в очередь — Иван Петров", step(func() error {
		_, err := b.tasks.Transition(ctx, b.member, k, task.TransitionInput{To: "todo"})
		return err
	}))
	_, err := b.tasks.Take(ctx, b.member, k)
	require.NoError(t, err)
	_, err = b.tasks.AddWorklog(ctx, b.member, k, task.WorklogInput{Duration: "2h 30m"})
	require.NoError(t, err)
	require.Equal(t, "🔍 "+k+" «Отчёт» отправлена на проверку — Иван Петров", step(func() error {
		_, err := b.tasks.Transition(ctx, b.member, k, task.TransitionInput{To: "review"})
		return err
	}))
	require.Equal(t, "🔁 "+k+" «Отчёт» возвращена на доработку — Анна Смирнова: нет тестов", step(func() error {
		_, err := b.tasks.Transition(ctx, b.manager, k, task.TransitionInput{To: "in_progress", Comment: "нет тестов"})
		return err
	}))
	_, err = b.tasks.Transition(ctx, b.member, k, task.TransitionInput{To: "review"})
	require.NoError(t, err)
	require.Equal(t, "✅ "+k+" «Отчёт» принята — Анна Смирнова. Списано 2h 30m из 3h", step(func() error {
		_, err := b.tasks.Transition(ctx, b.manager, k, task.TransitionInput{To: "done"})
		return err
	}))

	tk2 := b.createdCard(t, task.CreateInput{Title: "Лишняя"})
	require.Equal(t, "❌ "+tk2.Key+" «Лишняя» отменена — Анна Смирнова: дубль", step(func() error {
		_, err := b.tasks.Transition(ctx, b.manager, tk2.Key, task.TransitionInput{To: "cancelled", Comment: "дубль"})
		return err
	}))
	// Переходы вне таблицы §8.6 (вернуть в бэклог) в группу не пишутся.
	n := len(b.sent(t))
	_, err = b.tasks.Transition(ctx, b.manager, tk2.Key, task.TransitionInput{To: "todo"})
	require.NoError(t, err)
	b.events(t)
	b.flush(t)
	require.Len(t, b.sent(t), n)
}

// «💬 …» о комментарии — выключено по умолчанию, включённое приходит ответом на карточку (§8.6).
func TestGroupEventComment(t *testing.T) {
	b := newEventBot(t)
	tk := b.createdCard(t, task.CreateInput{Title: "Обсуждение"})
	_, err := b.tasks.AddComment(context.Background(), b.member, tk.Key, "по умолчанию не видно")
	require.NoError(t, err)
	b.events(t)
	b.flush(t)
	require.Empty(t, b.sent(t))

	require.NoError(t, b.g.Model(b.p).Update("notification_settings", `{"comment_created":true}`).Error)
	_, err = b.tasks.AddComment(context.Background(), b.member, tk.Key, "Готово, проверьте")
	require.NoError(t, err)
	b.events(t)
	b.flush(t)
	require.Equal(t, []map[string]string{{"text": "💬 Иван Петров к " + tk.Key + ": Готово, проверьте",
		"reply": itoa64(*b.messageID(t, tk)), "chat": itoa64(b.chatID)}}, b.sent(t))
}

// Запуск и завершение спринта (§8.6): сводка и свободные задачи с кнопками «Взять».
func TestGroupEventSprint(t *testing.T) {
	b := newEventBot(t)
	free := b.createdCard(t, task.CreateInput{Title: "Свободная", Priority: "critical"})
	taken := b.createdCard(t, task.CreateInput{Title: "Занятая"})
	var sid string
	require.NoError(t, b.g.Raw(`INSERT INTO sprints (project_id, name, start_date, end_date, state) VALUES (?, 'Спринт 7', '2026-10-13', '2026-10-24', 'active') RETURNING id::text`,
		b.p.ID).Scan(&sid).Error)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET sprint_id = ? WHERE id IN (?, ?)`, sid, free.ID, taken.ID).Error)
	require.NoError(t, b.g.Exec(`UPDATE tasks SET assignee_id = ? WHERE id = ?`, b.member.ID, taken.ID).Error)

	b.publish(t, events.SprintStarted, `{"sprint_id":"`+sid+`","project_id":"`+b.p.ID.String()+`","name":"Спринт 7",
		"start_date":"2026-10-13T00:00:00Z","end_date":"2026-10-24T00:00:00Z","tasks":18,"estimate_minutes":3840}`)
	b.publish(t, events.SprintClosed, `{"sprint_id":"`+sid+`","project_id":"`+b.p.ID.String()+`","name":"Спринт 7",
		"done":15,"total":18,"moved":3,"estimate_minutes":3840,"spent_minutes":3480}`)
	b.flush(t)
	sent := b.tg.Calls("sendMessage")
	start, closed := sent[len(sent)-2].Params, sent[len(sent)-1].Params
	require.Equal(t, "🚀 Спринт 7 запущен: 13.10–24.10.2026, 18 задач, оценка 64h\n\nСвободные задачи:\n"+free.Key+" «Свободная»", start["text"])
	require.JSONEq(t, `{"inline_keyboard":[[{"text":"Взять `+free.Key+`","callback_data":"take:`+free.ID.String()+`"}]]}`, start["reply_markup"])
	require.Equal(t, "🏁 Спринт 7 завершён: готово 15 из 18, перенесено 3, списано 58h из 64h", closed["text"])

	// Выключенные события спринта не публикуются.
	require.NoError(t, b.g.Model(b.p).Update("notification_settings", `{"sprint_started":false,"sprint_closed":false}`).Error)
	n := len(b.tg.Calls("sendMessage"))
	b.publish(t, events.SprintStarted, `{"sprint_id":"`+sid+`","project_id":"`+b.p.ID.String()+`","name":"Спринт 8","tasks":1}`)
	b.publish(t, events.SprintClosed, `{"sprint_id":"`+sid+`","project_id":"`+b.p.ID.String()+`","name":"Спринт 8"}`)
	b.flush(t)
	require.Len(t, b.tg.Calls("sendMessage"), n)
}

// publish — событие напрямую в обработчики (как от релея outbox).
func (b *cardBot) publish(t *testing.T, typ, payload string) {
	t.Helper()
	env, err := json.Marshal(events.Envelope{EventID: uuid.NewString(), Type: typ, Payload: json.RawMessage(payload)})
	require.NoError(t, err)
	require.NoError(t, b.d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+typ, env)))
}

func TestPluralAndDates(t *testing.T) {
	for n, want := range map[int]string{1: "1 задача", 2: "2 задачи", 5: "5 задач", 11: "11 задач", 12: "12 задач", 21: "21 задача", 22: "22 задачи", 111: "111 задач"} {
		require.Equal(t, want, telegram.Plural(n, "задача", "задачи", "задач"))
	}
	require.Equal(t, "28.12.2026–08.01.2027", telegram.DateRange("2026-12-28", "2027-01-08"))
	require.Equal(t, "", telegram.DateRange("", "2027-01-08"))
}

// События одной задачи обработаны не по порядку (взятие после отправки на проверку): в группу
// уходит сообщение о текущем состоянии, а не о последнем обработанном событии.
func TestGroupEventsOutOfOrder(t *testing.T) {
	b := newEventBot(t)
	tk := b.createdCard(t, task.CreateInput{Title: "Порядок"})
	_, err := b.tasks.Take(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	_, err = b.tasks.Transition(context.Background(), b.member, tk.Key, task.TransitionInput{To: "review"})
	require.NoError(t, err)
	by := b.member.ID.String()
	b.publish(t, events.TaskTransitioned, `{"task_id":"`+tk.ID.String()+`","from":"in_progress","to":"review","by":"`+by+`"}`)
	b.publish(t, events.TaskTransitioned, `{"task_id":"`+tk.ID.String()+`","from":"todo","to":"in_progress","by":"`+by+`"}`)
	b.flush(t)
	sent := b.sent(t)
	require.Len(t, sent, 1)
	require.Contains(t, sent[0]["text"], "отправлена на проверку")
}

// Сбой после сборки сообщения (задание повторяется с начала) не теряет событие: события
// забываются только после отправки.
func TestGroupEventSurvivesRetry(t *testing.T) {
	b := newEventBot(t)
	tk := b.createdCard(t, task.CreateInput{Title: "Повтор"})
	_, err := b.tasks.Transition(context.Background(), b.manager, tk.Key, task.TransitionInput{To: "cancelled", Comment: strings.Repeat("я", 600)})
	require.NoError(t, err)
	b.events(t)
	msgs := b.messages(t)
	b.tg.Respond("sendMessage", `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
	for _, m := range msgs {
		require.NoError(t, b.deliver(t, b.send, m)) // первая попытка не удалась, повтор поставлен
	}
	b.flush(t)
	sent := b.sent(t)
	require.Len(t, sent, 2, "первая попытка — ответ 500, повтор — тот же текст")
	require.Equal(t, sent[0], sent[1])
	require.Equal(t, "❌ "+tk.Key+" «Повтор» отменена — Анна Смирнова: "+strings.Repeat("я", 500)+"…", sent[1]["text"], "длинная причина обрезана")
	n, err := b.rdb.Exists(context.Background(), "tg:events:"+tk.ID.String()).Result()
	require.NoError(t, err)
	require.Zero(t, n, "после отправки события забыты")
}

// §12.4: переход от системы (GitHub) подписан «GitHub (логин)» из by_name.
func TestGroupEventSystemActor(t *testing.T) {
	b := newEventBot(t)
	tk := b.createdCard(t, task.CreateInput{Title: "Из GitHub"})
	_, err := b.tasks.Take(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	_, err = b.tasks.Transition(context.Background(), b.member, tk.Key, task.TransitionInput{To: "review"})
	require.NoError(t, err)
	b.publish(t, events.TaskTransitioned, `{"task_id":"`+tk.ID.String()+`","from":"in_progress","to":"review","by":"`+
		uuid.Nil.String()+`","by_name":"GitHub (octo)"}`)
	b.flush(t)
	sent := b.sent(t)
	require.Len(t, sent, 1)
	require.Contains(t, sent[0]["text"], "GitHub (octo)")
}
