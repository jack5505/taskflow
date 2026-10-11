package telegram_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/user"
)

// takeBot — группа с карточками и сообщениями о событиях и бот с командами взятия, как в воркере.
type takeBot struct {
	*cardBot
	bot  *telegram.Processor
	chat models.Chat
	upd  int64
}

func newTakeBot(t *testing.T) *takeBot {
	b := &takeBot{cardBot: newEventBot(t)}
	b.chat = models.Chat{ID: b.chatID, Type: models.ChatTypeGroup, Title: "Команда"}
	rt := telegram.NewRouter("TaskBot")
	telegram.RegisterAccount(rt, telegram.NewAccounts(b.g, "TaskBot"), b.rdb)
	telegram.RegisterTake(rt, telegram.NewTaskCommands(b.g, b.tasks, project.NewService(b.g), b.groups))
	b.bot = telegram.NewProcessor(b.api, rt, b.out)
	return b
}

func (b *takeBot) tgOf(t *testing.T, u *user.User) int64 {
	t.Helper()
	var id int64
	require.NoError(t, b.g.Raw(`SELECT telegram_user_id FROM users WHERE id = ?`, u.ID).Scan(&id).Error)
	return id
}

func (b *takeBot) command(from int64, chat models.Chat, text string) {
	b.upd++
	b.bot.Handle(context.Background(), &models.Update{ID: b.upd, Message: &models.Message{ID: 70, Text: text, Chat: chat,
		From: &models.User{ID: from}}}, time.Now())
}

func (b *takeBot) press(from int64, chat models.Chat, data, id string) {
	b.bot.Handle(context.Background(), &models.Update{CallbackQuery: &models.CallbackQuery{ID: id, Data: data, From: models.User{ID: from},
		Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 101, Chat: chat}}}}, time.Now())
}

// answers — ответы на нажатия кнопок: id нажатия → текст и всплывающее ли окно.
func (b *takeBot) answers() map[string][2]string {
	out := map[string][2]string{}
	for _, c := range b.tg.Calls("answerCallbackQuery") {
		out[c.Params["callback_query_id"]] = [2]string{c.Params["text"], c.Params["show_alert"]}
	}
	return out
}

func (b *takeBot) assignee(t *testing.T, tk *task.Task) (string, string) {
	t.Helper()
	var r struct{ Status, Name string }
	require.NoError(t, b.g.Raw(`SELECT t.status, COALESCE(u.display_name, '') AS name FROM tasks t LEFT JOIN users u ON u.id = t.assignee_id WHERE t.id = ?`,
		tk.ID).Scan(&r).Error)
	return r.Status, r.Name
}

// Сценарий приёмки §10.4: два исполнителя одновременно нажимают «Взять в работу» — задачу получает
// один, второй видит «Задача … уже в работе, исполнитель: …», в группе ровно одно сообщение
// «… взята в работу — …»; в истории переход с source = telegram.
func TestTakeButtonConcurrent(t *testing.T) {
	b := newTakeBot(t)
	other, otherTG := b.linked(t, "Пётр Сидоров")
	require.NoError(t, b.g.Create(&project.Member{ProjectID: b.p.ID, UserID: other.ID, Role: project.RoleMember}).Error)
	tk := b.createdCard(t, task.CreateInput{Title: "Гонка"})
	memberTG := b.tgOf(t, b.member)

	var wg sync.WaitGroup
	wg.Go(func() { b.press(memberTG, b.chat, "take:"+tk.ID.String(), "cb-member") })
	wg.Go(func() { b.press(otherTG, b.chat, "take:"+tk.ID.String(), "cb-other") })
	wg.Wait()

	status, name := b.assignee(t, tk)
	require.Equal(t, "in_progress", status)
	answers := b.answers()
	winner, loser := "cb-member", "cb-other"
	if name == "Пётр Сидоров" {
		winner, loser = loser, winner
	}
	require.Equal(t, [2]string{"🛠 " + tk.Key + " «Гонка» взята в работу", ""}, answers[winner])
	require.Equal(t, [2]string{"Задача " + tk.Key + " уже в работе, исполнитель: " + name, "true"}, answers[loser])

	b.messages(t) // ответы на нажатия ушли через answerCallbackQuery, в очереди их нет
	b.events(t)
	b.flush(t)
	var taken []string
	for _, m := range b.sent(t) {
		if strings.Contains(m["text"], "взята в работу") {
			taken = append(taken, m["text"])
		}
	}
	require.Len(t, taken, 1, "в группе ровно одно сообщение о взятии")
	require.Contains(t, taken[0], "— "+name)

	var sources []string
	require.NoError(t, b.g.Raw(`SELECT source FROM task_history WHERE task_id = ? AND field = 'status' AND new_value = '"in_progress"'`, tk.ID).Scan(&sources).Error)
	require.Equal(t, []string{"telegram"}, sources)
}

// /take TM-5 и /take 5 равнозначны в группе с одним проектом; отказы — ответом, который бот
// удаляет через 30 секунд (§8.4, §8.5).
func TestTakeCommand(t *testing.T) {
	b := newTakeBot(t)
	memberTG := b.tgOf(t, b.member)
	t1 := b.createdCard(t, task.CreateInput{Title: "Первая"})
	t2 := b.createdCard(t, task.CreateInput{Title: "Вторая"})
	b.messages(t)

	b.command(memberTG, b.chat, "/take "+strings.ToLower(t1.Key))
	b.command(memberTG, b.chat, "/take@TaskBot 0"+strings.TrimPrefix(t2.Key, b.p.Key+"-"))
	for _, tk := range []*task.Task{t1, t2} {
		status, name := b.assignee(t, tk)
		require.Equal(t, "in_progress", status)
		require.Equal(t, "Иван Петров", name)
	}
	require.Empty(t, b.messages(t), "в группе об успехе пишет сообщение о событии, не ответ на команду")

	reply := func(from int64, chat models.Chat, text string) telegram.Message {
		t.Helper()
		b.command(from, chat, text)
		msgs := b.messages(t)
		require.Len(t, msgs, 1, text)
		return msgs[0]
	}
	b.command(memberTG, b.chat, "/take "+t1.Key)
	again := b.messages(t)
	require.Len(t, again, 1, "своя задача уже в работе — повтор без изменений, подтверждение временным ответом")
	require.Equal(t, 30, again[0].DeleteAfter)
	m := reply(memberTG, b.chat, "/take 999")
	require.Equal(t, 30, m.DeleteAfter, "отказ в группе удаляется через 30 секунд")
	require.Equal(t, 70, m.ReplyTo)

	_, err := b.tasks.Transition(context.Background(), b.member, t2.Key, task.TransitionInput{To: "review"})
	require.NoError(t, err)
	require.Equal(t, "Задача "+t2.Key+" в статусе На проверке, взять нельзя", reply(memberTG, b.chat, "/take "+t2.Key).Text)
	require.Equal(t, "Задача "+b.p.Key+"-999 не найдена", reply(memberTG, b.chat, "/take 999").Text)
	require.Equal(t, "Формат: /take TM-42", reply(memberTG, b.chat, "/take").Text)
	require.Equal(t, "Вы не участник проекта ZZ", reply(memberTG, b.chat, "/take ZZ-1").Text, "проект не этой группы (§8.4, шаг 2)")

	_, outsiderTG := b.linked(t, "Посторонний")
	require.Equal(t, "Вы не участник проекта "+b.p.Key, reply(outsiderTG, b.chat, "/take "+t1.Key).Text)
	require.Equal(t, "Сначала привяжите аккаунт: профиль → Привязать Telegram", reply(tgID(), b.chat, "/take "+t1.Key).Text)

	// Личный чат: ключ проекта обязателен, успех — ответом.
	private := models.Chat{ID: memberTG, Type: models.ChatTypePrivate}
	t3 := b.createdCard(t, task.CreateInput{Title: "Третья"})
	b.messages(t)
	require.Equal(t, "Формат: /take TM-42", reply(memberTG, private, "/take 3").Text)
	require.Equal(t, "🛠 "+t3.Key+" «Третья» взята в работу", reply(memberTG, private, "/take "+t3.Key).Text)
	require.Equal(t, "Задача "+t3.Key+" не найдена", reply(outsiderTG, models.Chat{ID: outsiderTG, Type: models.ChatTypePrivate}, "/take "+t3.Key).Text,
		"в личном чате чужой проект не раскрывается")

	// Архив.
	t4 := b.createdCard(t, task.CreateInput{Title: "Архивная"})
	b.messages(t)
	require.NoError(t, b.g.Model(b.p).Update("is_archived", true).Error)
	require.Equal(t, "Проект "+b.p.Key+" в архиве, изменения недоступны", reply(memberTG, b.chat, "/take "+t4.Key).Text)
}

// Группа с двумя проектами (§10.4): /take 5 без ключа — подсказка со списком проектов, /take TM-5 работает.
func TestTakeInGroupWithTwoProjects(t *testing.T) {
	b := newTakeBot(t)
	memberTG, mgrTG := b.tgOf(t, b.member), b.tgOf(t, b.manager)
	second := b.project(t, "Второй", map[*user.User]string{b.manager: project.RoleManager, b.member: project.RoleMember})
	b.say(mgrTG, b.chat, "/bind "+b.code(t, b.manager, second))
	tk := b.createdCard(t, task.CreateInput{Title: "Задача"})
	b.messages(t)

	b.command(memberTG, b.chat, "/take 1")
	msgs := b.messages(t)
	require.Len(t, msgs, 1)
	first, other := b.p.Key, second.Key
	if other < first {
		first, other = other, first
	}
	require.Equal(t, "К группе подключено несколько проектов: "+first+", "+other+". Укажите ключ задачи: /take "+first+"-1", msgs[0].Text)

	b.command(memberTG, b.chat, "/take "+tk.Key)
	status, _ := b.assignee(t, tk)
	require.Equal(t, "in_progress", status)
}

// Кнопка из личного уведомления о назначении (§8.7) — то же взятие; кнопка удалённой задачи и
// битые данные — «Задача не найдена».
func TestTakeButtonPrivate(t *testing.T) {
	b := newTakeBot(t)
	memberTG := b.tgOf(t, b.member)
	tk := b.createdCard(t, task.CreateInput{Title: "Личная"})
	private := models.Chat{ID: memberTG, Type: models.ChatTypePrivate}
	b.press(memberTG, private, "take:"+tk.ID.String(), "cb-1")
	b.press(memberTG, private, "take:not-a-uuid", "cb-2")
	require.NoError(t, b.tasks.Delete(context.Background(), b.manager, tk.Key))
	b.press(memberTG, private, "take:"+tk.ID.String(), "cb-3")
	answers := b.answers()
	require.Equal(t, "🛠 "+tk.Key+" «Личная» взята в работу", answers["cb-1"][0])
	require.Equal(t, [2]string{"Задача не найдена", "true"}, answers["cb-2"])
	require.Equal(t, [2]string{"Задача не найдена", "true"}, answers["cb-3"])
}

// Успех в группе без сообщения о событии (событие «взята в работу» выключено в проекте или задача
// уже была в работе без исполнителя) — бот отвечает сам временным ответом.
func TestTakeReplyWhenNotAnnounced(t *testing.T) {
	b := newTakeBot(t)
	memberTG := b.tgOf(t, b.member)
	t1 := b.createdCard(t, task.CreateInput{Title: "В работе без исполнителя"})
	require.NoError(t, b.g.Exec(`UPDATE tasks SET status = 'in_progress' WHERE id = ?`, t1.ID).Error)
	b.messages(t)
	b.command(memberTG, b.chat, "/take "+t1.Key)
	msgs := b.messages(t)
	require.Len(t, msgs, 1)
	require.Equal(t, "🛠 "+t1.Key+" «В работе без исполнителя» взята в работу", msgs[0].Text)
	require.Equal(t, 30, msgs[0].DeleteAfter)

	require.NoError(t, b.g.Model(b.p).Update("notification_settings", `{"task_taken":false}`).Error)
	t2 := b.createdCard(t, task.CreateInput{Title: "Тихо"})
	b.messages(t)
	b.command(memberTG, b.chat, "/take "+t2.Key)
	msgs = b.messages(t)
	require.Len(t, msgs, 1)
	require.Equal(t, "🛠 "+t2.Key+" «Тихо» взята в работу", msgs[0].Text)
}
