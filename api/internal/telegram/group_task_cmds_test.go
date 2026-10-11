package telegram_test

import (
	"context"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
)

// one — единственный ответ бота на команду (с удалением из очереди).
func (b *takeBot) one(t *testing.T, from int64, chat models.Chat, text string) telegram.Message {
	t.Helper()
	b.command(from, chat, text)
	msgs := b.messages(t)
	require.Len(t, msgs, 1, text)
	return msgs[0]
}

func (b *takeBot) private(id int64) models.Chat {
	return models.Chat{ID: id, Type: models.ChatTypePrivate}
}

// Сценарий приёмки §10.4: исполнитель списывает 1h 30m через /log и 45m через веб; в карточке
// сумма 2h 15m. Ответ в группе удаляется через 30 секунд, запись — с source = telegram.
func TestLogCommand(t *testing.T) {
	b := newTakeBot(t)
	memberTG := b.tgOf(t, b.member)
	est := 180
	tk := b.createdCard(t, task.CreateInput{Title: "Отчёт", EstimateMinutes: &est})
	_, err := b.tasks.Take(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	b.messages(t)

	m := b.one(t, memberTG, b.chat, "/log "+tk.Key+" 1h 30m починил экспорт")
	require.Equal(t, "Списано 1h 30m на "+tk.Key+". Всего 1h 30m из 3h", m.Text)
	require.Equal(t, 30, m.DeleteAfter)
	_, err = b.tasks.AddWorklog(context.Background(), b.member, tk.Key, task.WorklogInput{Duration: "45m"})
	require.NoError(t, err)
	got, _, err := b.tasks.Get(context.Background(), b.member, tk.Key)
	require.NoError(t, err)
	require.Equal(t, 135, got.SpentMinutes, "2h 15m")
	var rows []struct{ Source, Comment string }
	require.NoError(t, b.g.Raw(`SELECT source, COALESCE(comment, '') AS comment FROM worklogs WHERE task_id = ? ORDER BY minutes DESC`, tk.ID).Scan(&rows).Error)
	require.Equal(t, []struct{ Source, Comment string }{{"telegram", "починил экспорт"}, {"manual", ""}}, rows)

	m = b.one(t, memberTG, b.private(memberTG), "/log "+tk.Key+" 15m")
	require.Equal(t, "Списано 15m на "+tk.Key+". Всего 2h 30m из 3h", m.Text)
	require.Zero(t, m.DeleteAfter, "в личном чате ответ не удаляется")

	for _, bad := range []string{"/log", "/log " + tk.Key, "/log " + tk.Key + " много"} {
		require.Equal(t, "Формат: /log TM-42 1h 30m [комментарий]", b.one(t, memberTG, b.chat, bad).Text, bad)
	}
}

// /drop, /review, /done — переходы §4 сервисом; /done исполнителем-автором отклоняется.
func TestTransitionCommands(t *testing.T) {
	b := newTakeBot(t)
	memberTG, mgrTG := b.tgOf(t, b.member), b.tgOf(t, b.manager)
	ctx := context.Background()
	own, err := b.tasks.Create(ctx, b.access(t, b.member, b.p), b.member, task.CreateInput{Title: "Своя"})
	require.NoError(t, err)
	b.events(t)
	b.flush(t)
	b.messages(t)

	b.command(memberTG, b.chat, "/take "+own.Key)
	require.Equal(t, "Задача "+own.Key+" в статусе В работе, команда недоступна", b.one(t, memberTG, b.chat, "/done "+own.Key).Text)
	b.command(memberTG, b.chat, "/review "+own.Key)
	require.Empty(t, b.messages(t), "в группе об успехе пишет сообщение о событии")
	require.Equal(t, "Свою задачу, где вы и автор, и исполнитель, принимает менеджер", b.one(t, memberTG, b.chat, "/done "+own.Key).Text)

	m := b.one(t, mgrTG, b.private(mgrTG), "/done "+own.Key)
	require.Equal(t, "✅ "+own.Key+" «Своя» принята. Списано 0m", m.Text)
	st, _ := b.assignee(t, own)
	require.Equal(t, "done", st)

	tk := b.createdCard(t, task.CreateInput{Title: "Брошенная"})
	b.command(memberTG, b.chat, "/take "+tk.Key)
	require.Equal(t, "↩️ "+tk.Key+" «Брошенная» возвращена в очередь", b.one(t, memberTG, b.private(memberTG), "/drop "+tk.Key).Text)
	st, name := b.assignee(t, tk)
	require.Equal(t, "todo", st)
	require.Empty(t, name)

	// Событие выключено — бот отвечает сам, временным ответом.
	require.NoError(t, b.g.Model(b.p).Update("notification_settings", `{"task_review":false}`).Error)
	b.command(memberTG, b.chat, "/take "+tk.Key)
	b.messages(t)
	m = b.one(t, memberTG, b.chat, "/review "+tk.Key)
	require.Equal(t, "🔍 "+tk.Key+" «Брошенная» отправлена на проверку", m.Text)
	require.Equal(t, 30, m.DeleteAfter)
	require.Equal(t, "Формат: /review TM-42", b.one(t, memberTG, b.chat, "/review").Text)
}

// /comment — комментарий с source = telegram (§3.6, §8.5).
func TestCommentCommand(t *testing.T) {
	b := newTakeBot(t)
	memberTG := b.tgOf(t, b.member)
	tk := b.createdCard(t, task.CreateInput{Title: "Обсуждение"})
	b.messages(t)
	require.Equal(t, "Комментарий добавлен к "+tk.Key, b.one(t, memberTG, b.chat, "/comment "+tk.Key+" Готово, проверьте").Text)
	var rows []struct{ Body, Source string }
	require.NoError(t, b.g.Raw(`SELECT body, source FROM comments WHERE task_id = ?`, tk.ID).Scan(&rows).Error)
	require.Equal(t, []struct{ Body, Source string }{{"Готово, проверьте", "telegram"}}, rows)
	require.Equal(t, "Формат: /comment TM-42 <текст>", b.one(t, memberTG, b.chat, "/comment "+tk.Key).Text)
}

// /task — карточка §8.3: в группе — новая текущая карточка задачи, в личном чате — показ.
func TestTaskCommand(t *testing.T) {
	b := newTakeBot(t)
	memberTG := b.tgOf(t, b.member)
	tk := b.createdCard(t, task.CreateInput{Title: "Показать"})
	old := *b.messageID(t, tk)
	b.messages(t)

	b.command(memberTG, b.chat, "/task "+tk.Key)
	require.Equal(t, 1, b.flush(t))
	sends := b.tg.Calls("sendMessage")
	last := sends[len(sends)-1].Params
	require.Equal(t, "HTML", last["parse_mode"])
	require.Contains(t, last["text"], "🆕 Новая задача "+tk.Key)
	require.NotEqual(t, old, *b.messageID(t, tk), "дальнейшие изменения правят новую карточку")

	m := b.one(t, memberTG, b.private(memberTG), "/task "+tk.Key)
	require.True(t, m.HTML)
	require.Contains(t, m.Text, "<b>Показать</b>")
	require.NotNil(t, m.Keyboard)
	require.Equal(t, memberTG, m.ChatID)

	require.NoError(t, b.tasks.Delete(context.Background(), b.manager, tk.Key))
	require.Equal(t, "Задача "+tk.Key+" не найдена", b.one(t, memberTG, b.chat, "/task "+tk.Key).Text)
}
