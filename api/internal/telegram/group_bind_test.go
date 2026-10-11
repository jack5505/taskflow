package telegram_test

import (
	"context"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// groupBot — бот с привязкой аккаунтов и командами группы, как в воркере.
type groupBot struct {
	*env
	g      *gorm.DB
	groups *telegram.Groups
	p      *telegram.Processor
	upd    int64
}

func newGroupBot(t *testing.T) *groupBot {
	e := newEnv(t)
	g := testutil.SharedDB(t)
	b := &groupBot{env: e, g: g, groups: telegram.NewGroups(g, "@TaskBot")}
	rt := telegram.NewRouter("TaskBot")
	telegram.RegisterAccount(rt, telegram.NewAccounts(g, "TaskBot"), e.rdb)
	telegram.RegisterGroups(rt, b.groups, e.rdb)
	b.p = telegram.NewProcessor(e.api, rt, e.out)
	return b
}

// linked — активный пользователь с привязанным Telegram; возвращает его и telegram_user_id.
func (b *groupBot) linked(t *testing.T, name string) (*user.User, int64) {
	t.Helper()
	u := newUser(t, b.g, name)
	tg := tgID()
	require.NoError(t, b.g.Model(u).Update("telegram_user_id", tg).Error)
	return u, tg
}

// project — проект с участниками: role по пользователю.
func (b *groupBot) project(t *testing.T, name string, members map[*user.User]string) *project.Project {
	t.Helper()
	p := project.New(testutil.UniqueKey(t), name)
	require.NoError(t, b.g.Create(p).Error)
	for u, role := range members {
		require.NoError(t, b.g.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: role}).Error)
	}
	return p
}

func (b *groupBot) access(t *testing.T, u *user.User, p *project.Project) *project.Access {
	t.Helper()
	a, err := project.NewService(b.g).Access(context.Background(), u, p.Key)
	require.NoError(t, err)
	return a
}

func (b *groupBot) code(t *testing.T, u *user.User, p *project.Project) string {
	t.Helper()
	c, err := b.groups.IssueBindCode(context.Background(), b.access(t, u, p))
	require.NoError(t, err)
	return c.Code
}

// say — команда text от Telegram-пользователя from в группе chat.
func (b *groupBot) say(from int64, chat models.Chat, text string) {
	b.upd++
	b.p.Handle(context.Background(), &models.Update{ID: b.upd, Message: &models.Message{ID: 7, Text: text, Chat: chat,
		From: &models.User{ID: from}}}, time.Now())
}

// service — служебное обновление чата.
func (b *groupBot) service(upd *models.Update) {
	b.upd++
	upd.ID = b.upd
	b.p.Handle(context.Background(), upd, time.Now())
}

func (b *groupBot) replies(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range b.messages(t) {
		out = append(out, m.Text)
	}
	return out
}

func (b *groupBot) reload(t *testing.T, p *project.Project) *project.Project {
	t.Helper()
	var out project.Project
	require.NoError(t, b.g.Unscoped().First(&out, "id = ?", p.ID).Error)
	return &out
}

func group(title string) models.Chat {
	return models.Chat{ID: -1_000_000 - testutil.Next(), Type: models.ChatTypeGroup, Title: title}
}

// §8.2, шаг 1: код из 8 символов на 15 минут, новый гасит прежний; выдаёт только менеджер
// (или администратор), в архивном проекте — нельзя.
func TestIssueBindCode(t *testing.T) {
	b := newGroupBot(t)
	ctx := context.Background()
	manager, _ := b.linked(t, "Анна Смирнова")
	member, _ := b.linked(t, "Иван Петров")
	p := b.project(t, "Тайм-менеджмент", map[*user.User]string{manager: project.RoleManager, member: project.RoleMember})

	c, err := b.groups.IssueBindCode(ctx, b.access(t, manager, p))
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`^[A-Z0-9]{8}$`), c.Code)
	require.WithinDuration(t, time.Now().Add(15*time.Minute), c.ExpiresAt, 5*time.Second)
	require.Equal(t, "/bind "+c.Code, c.Command)
	require.Equal(t, "TaskBot", c.BotUsername)

	c2, err := b.groups.IssueBindCode(ctx, b.access(t, manager, p))
	require.NoError(t, err)
	_, err = b.groups.Bind(ctx, c.Code, -100, "Группа", manager.ID)
	require.ErrorIs(t, err, telegram.ErrCodeInvalid, "прежний код погашен")
	require.NotEqual(t, c.Code, c2.Code)

	_, err = b.groups.IssueBindCode(ctx, b.access(t, member, p))
	require.ErrorIs(t, err, telegram.ErrNotManager)
	// Исключён из проекта между проверкой доступа и блокировкой — 404, как не участнику (§2).
	memberAccess := b.access(t, member, p)
	require.NoError(t, b.g.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, p.ID, member.ID).Error)
	_, err = b.groups.IssueBindCode(ctx, memberAccess)
	require.ErrorIs(t, err, project.ErrNotFound)

	admin := newUser(t, b.g, "Админ")
	require.NoError(t, b.g.Model(admin).Update("is_system_admin", true).Error)
	admin.IsSystemAdmin = true
	_, err = b.groups.IssueBindCode(ctx, b.access(t, admin, p))
	require.NoError(t, err, "администратор системы подключает группы (§2)")

	require.NoError(t, b.g.Model(p).Update("is_archived", true).Error)
	_, err = b.groups.IssueBindCode(ctx, b.access(t, manager, p))
	require.ErrorIs(t, err, project.ErrArchived)
}

// Сценарий §8.2: код из настроек → /bind <код> в группе → «Группа подключена к проекту …»,
// telegram_chat_id и название группы записаны, код погашен, действие в журнале.
func TestBindCommand(t *testing.T) {
	b := newGroupBot(t)
	manager, mgrTG := b.linked(t, "Анна Смирнова")
	p := b.project(t, "Тайм-менеджмент", map[*user.User]string{manager: project.RoleManager})
	chat := group("Команда TM")
	code := b.code(t, manager, p)

	b.say(mgrTG, chat, "/bind@TaskBot "+code)
	require.Equal(t, []string{"Группа подключена к проекту " + p.Key + " «Тайм-менеджмент». Команды: /help"}, b.replies(t))
	got := b.reload(t, p)
	require.Equal(t, chat.ID, *got.TelegramChatID)
	require.Equal(t, "Команда TM", *got.TelegramChatTitle)
	var audit []struct {
		Action, Source string
		ActorID        uuid.UUID
	}
	require.NoError(t, b.g.Raw(`SELECT action, source, actor_id FROM audit_logs WHERE project_id = ?`, p.ID).Scan(&audit).Error)
	require.Len(t, audit, 1)
	require.Equal(t, telegram.AuditGroupBound, audit[0].Action)
	require.Equal(t, "bot", audit[0].Source)
	require.Equal(t, manager.ID, audit[0].ActorID)

	// Код одноразовый.
	b.say(mgrTG, chat, "/bind "+code)
	require.Equal(t, []string{"Код не найден или устарел. Получите новый в настройках проекта → Telegram"}, b.replies(t))

	// Повторное подключение той же группы новым кодом — успех без изменений.
	b.say(mgrTG, chat, "/bind "+b.code(t, manager, p))
	require.Equal(t, []string{"Группа подключена к проекту " + p.Key + " «Тайм-менеджмент». Команды: /help"}, b.replies(t))
}

// §8.2, шаг 3: при нарушении любого условия бот отвечает причиной и ничего не меняет.
func TestBindRejects(t *testing.T) {
	b := newGroupBot(t)
	manager, mgrTG := b.linked(t, "Анна Смирнова")
	member, memberTG := b.linked(t, "Иван Петров")
	_, outsiderTG := b.linked(t, "Посторонний")
	p := b.project(t, "Проект", map[*user.User]string{manager: project.RoleManager, member: project.RoleMember})
	chat := group("Группа")
	code := b.code(t, manager, p)

	b.say(tgID(), chat, "/bind "+code)
	require.Equal(t, []string{"Сначала привяжите аккаунт: профиль → Привязать Telegram"}, b.replies(t), "не привязан")
	b.say(memberTG, chat, "/bind "+code)
	require.Equal(t, []string{"Подключать и отключать группу может только менеджер проекта"}, b.replies(t), "исполнитель")
	b.say(outsiderTG, chat, "/bind "+code)
	require.Equal(t, []string{"Подключать и отключать группу может только менеджер проекта"}, b.replies(t), "не участник")
	b.say(mgrTG, models.Chat{ID: mgrTG, Type: models.ChatTypePrivate}, "/bind "+code)
	require.Equal(t, []string{"Эта команда работает в группе проекта"}, b.replies(t), "личный чат")
	b.say(mgrTG, chat, "/bind")
	require.Equal(t, []string{"Формат: /bind <код>. Код выдаётся в настройках проекта → Telegram"}, b.replies(t))
	require.Nil(t, b.reload(t, p).TelegramChatID, "ничего не изменилось")

	// Истёкший код.
	b.groups.SetClock(func() time.Time { return time.Now().Add(16 * time.Minute) })
	b.say(mgrTG, chat, "/bind "+code)
	require.Equal(t, []string{"Код не найден или устарел. Получите новый в настройках проекта → Telegram"}, b.replies(t))
	b.groups.SetClock(time.Now)

	// У проекта уже есть другая группа.
	other := group("Другая")
	require.NoError(t, b.g.Model(p).Update("telegram_chat_id", other.ID).Error)
	b.say(mgrTG, chat, "/bind "+code)
	require.Equal(t, []string{"К проекту " + p.Key + " уже подключена другая группа. Сначала отключите её: /unbind в той группе или кнопка в настройках проекта"}, b.replies(t))
	require.Equal(t, other.ID, *b.reload(t, p).TelegramChatID)

	// Архивный проект.
	require.NoError(t, b.g.Model(p).Updates(map[string]any{"telegram_chat_id": nil, "is_archived": true}).Error)
	b.say(mgrTG, chat, "/bind "+code)
	require.Equal(t, []string{"Проект " + p.Key + " в архиве, изменения недоступны"}, b.replies(t))
	require.Nil(t, b.reload(t, p).TelegramChatID)
}

// Перебор кодов /bind ограничен, как у /start: после 10 неверных кодов за час — отказ без проверки.
func TestBindLimitsCodeGuessing(t *testing.T) {
	b := newGroupBot(t)
	manager, mgrTG := b.linked(t, "Анна Смирнова")
	p := b.project(t, "Проект", map[*user.User]string{manager: project.RoleManager})
	chat := group("Группа")
	for range 10 {
		b.say(mgrTG, chat, "/bind WRONG000")
	}
	b.messages(t)
	b.say(mgrTG, chat, "/bind "+b.code(t, manager, p))
	require.Equal(t, []string{"Слишком много неверных кодов. Попробуйте через час"}, b.replies(t))
	require.Nil(t, b.reload(t, p).TelegramChatID)
}

// Одна группа на несколько проектов (§8.2, §8.5): /unbind без ключа — подсказка со списком,
// /unbind <ключ> отключает один проект; с одним проектом ключ не нужен.
func TestUnbindCommand(t *testing.T) {
	b := newGroupBot(t)
	manager, mgrTG := b.linked(t, "Анна Смирнова")
	member, memberTG := b.linked(t, "Иван Петров")
	members := map[*user.User]string{manager: project.RoleManager, member: project.RoleMember}
	p1 := b.project(t, "Первый", members)
	p2 := b.project(t, "Второй", members)
	chat := group("Общая")
	b.say(mgrTG, chat, "/bind "+b.code(t, manager, p1))
	b.say(mgrTG, chat, "/bind "+b.code(t, manager, p2))
	b.messages(t)
	first, second := p1, p2
	if p2.Key < p1.Key {
		first, second = p2, p1
	}

	b.say(mgrTG, chat, "/unbind")
	require.Equal(t, []string{"К группе подключено несколько проектов: " + first.Key + ", " + second.Key + ". Укажите ключ: /unbind " + first.Key}, b.replies(t))
	b.say(mgrTG, chat, "/unbind ZZZZZZZZZZ")
	require.Equal(t, []string{"Проект ZZZZZZZZZZ не подключён к этой группе"}, b.replies(t))
	b.say(memberTG, chat, "/unbind "+p1.Key)
	require.Equal(t, []string{"Подключать и отключать группу может только менеджер проекта"}, b.replies(t))

	// Номер сообщения задачи — из этой группы; после отключения он чужой и забывается.
	taskID := newTask(t, b.g, p1, manager, 555)
	b.say(mgrTG, chat, "/unbind "+p1.Key)
	require.Equal(t, []string{"Группа отключена от проекта " + p1.Key + " «Первый»"}, b.replies(t))
	require.Nil(t, b.reload(t, p1).TelegramChatID)
	require.Nil(t, b.reload(t, p1).TelegramChatTitle)
	require.Nil(t, messageID(t, b.g, taskID))
	require.Equal(t, chat.ID, *b.reload(t, p2).TelegramChatID, "второй проект остался")

	b.say(mgrTG, chat, "/unbind")
	require.Equal(t, []string{"Группа отключена от проекта " + p2.Key + " «Второй»"}, b.replies(t))
	b.say(mgrTG, chat, "/unbind")
	require.Equal(t, []string{"К этой группе не подключён ни один проект"}, b.replies(t))
	b.say(mgrTG, models.Chat{ID: mgrTG, Type: models.ChatTypePrivate}, "/unbind")
	require.Equal(t, []string{"Эта команда работает в группе проекта"}, b.replies(t))
}

// Сценарий приёмки §10.4 «удаление бота из группы»: привязка снимается у всех проектов группы,
// их менеджеры получают личное уведомление; проекты других групп не меняются.
func TestBotRemovedFromGroup(t *testing.T) {
	b := newGroupBot(t)
	m1, m1TG := b.linked(t, "Анна Смирнова")
	m2, m2TG := b.linked(t, "Пётр Иванов")
	member, _ := b.linked(t, "Иван Петров")
	unlinked := newUser(t, b.g, "Без Telegram")
	p1 := b.project(t, "Первый", map[*user.User]string{m1: project.RoleManager, member: project.RoleMember, unlinked: project.RoleManager})
	p2 := b.project(t, "Второй", map[*user.User]string{m1: project.RoleManager, m2: project.RoleManager})
	p3 := b.project(t, "Третий", map[*user.User]string{m1: project.RoleManager})
	chat, other := group("Общая"), group("Другая")
	for _, p := range []*project.Project{p1, p2} {
		b.say(m1TG, chat, "/bind "+b.code(t, m1, p))
	}
	b.say(m1TG, other, "/bind "+b.code(t, m1, p3))
	b.messages(t)
	taskID := newTask(t, b.g, p1, m1, 777)

	b.service(&models.Update{MyChatMember: &models.ChatMemberUpdated{Chat: chat, From: models.User{ID: m2TG},
		OldChatMember: models.ChatMember{Type: models.ChatMemberTypeMember, Member: &models.ChatMemberMember{}},
		NewChatMember: models.ChatMember{Type: models.ChatMemberTypeBanned, Banned: &models.ChatMemberBanned{}}}})

	require.Nil(t, b.reload(t, p1).TelegramChatID)
	require.Nil(t, b.reload(t, p2).TelegramChatID)
	require.Equal(t, other.ID, *b.reload(t, p3).TelegramChatID)
	require.Nil(t, messageID(t, b.g, taskID))

	sent := map[int64]string{}
	for _, m := range b.messages(t) {
		sent[m.ChatID] = m.Text
	}
	first, second := p1, p2
	if p2.Key < p1.Key {
		first, second = p2, p1
	}
	require.Equal(t, map[int64]string{
		m1TG: "Бота удалили из группы «Общая», Telegram-группа отключена от проектов:\n" +
			first.Key + " «" + first.Name + "»\n" + second.Key + " «" + second.Name + "»\n" +
			"Подключить группу снова можно в настройках проекта → Telegram",
		m2TG: "Бота удалили из группы «Общая», Telegram-группа отключена от проекта:\n" +
			p2.Key + " «Второй»\nПодключить группу снова можно в настройках проекта → Telegram",
	}, sent, "только менеджеры с привязанным Telegram")

	// Бот вышел сам (left) из группы без проектов — ничего не происходит.
	b.service(&models.Update{MyChatMember: &models.ChatMemberUpdated{Chat: chat,
		NewChatMember: models.ChatMember{Type: models.ChatMemberTypeLeft, Left: &models.ChatMemberLeft{}}}})
	require.Empty(t, b.messages(t))
	// Бота добавили в группу снова — привязка сама не возвращается.
	b.service(&models.Update{MyChatMember: &models.ChatMemberUpdated{Chat: chat,
		NewChatMember: models.ChatMember{Type: models.ChatMemberTypeMember, Member: &models.ChatMemberMember{}}}})
	require.Nil(t, b.reload(t, p1).TelegramChatID)
	// Ограничен без членства в группе — тоже удалён.
	b.service(&models.Update{MyChatMember: &models.ChatMemberUpdated{Chat: other,
		NewChatMember: models.ChatMember{Type: models.ChatMemberTypeRestricted, Restricted: &models.ChatMemberRestricted{IsMember: true}}}})
	require.Equal(t, other.ID, *b.reload(t, p3).TelegramChatID, "ограничен, но в группе")
	b.service(&models.Update{MyChatMember: &models.ChatMemberUpdated{Chat: other,
		NewChatMember: models.ChatMember{Type: models.ChatMemberTypeRestricted, Restricted: &models.ChatMemberRestricted{IsMember: false}}}})
	require.Nil(t, b.reload(t, p3).TelegramChatID)
	b.messages(t)
	require.NoError(t, b.g.Model(p3).Update("telegram_chat_id", other.ID).Error)
	// Пользователь остановил бота в личном чате — это не группа.
	b.service(&models.Update{MyChatMember: &models.ChatMemberUpdated{Chat: models.Chat{ID: other.ID, Type: models.ChatTypePrivate},
		NewChatMember: models.ChatMember{Type: models.ChatMemberTypeBanned, Banned: &models.ChatMemberBanned{}}}})
	require.Equal(t, other.ID, *b.reload(t, p3).TelegramChatID)
}

// §8.2, шаг 6: группа стала супергруппой — telegram_chat_id всех её проектов переписывается,
// второе служебное сообщение ничего не меняет; переименование группы обновляет название.
func TestGroupMigrationAndRename(t *testing.T) {
	b := newGroupBot(t)
	m, mTG := b.linked(t, "Анна Смирнова")
	p1 := b.project(t, "Первый", map[*user.User]string{m: project.RoleManager})
	p2 := b.project(t, "Второй", map[*user.User]string{m: project.RoleManager})
	chat := group("Старая")
	for _, p := range []*project.Project{p1, p2} {
		b.say(mTG, chat, "/bind "+b.code(t, m, p))
	}
	b.messages(t)
	taskID := newTask(t, b.g, p1, m, 42)
	super := models.Chat{ID: -1_009_000_000_000 - testutil.Next(), Type: models.ChatTypeSupergroup, Title: "Старая"}

	b.service(&models.Update{Message: &models.Message{ID: 9, Chat: chat, From: &models.User{ID: mTG}, MigrateToChatID: super.ID}})
	b.service(&models.Update{Message: &models.Message{ID: 1, Chat: super, From: &models.User{ID: mTG}, MigrateFromChatID: chat.ID}})
	require.Equal(t, super.ID, *b.reload(t, p1).TelegramChatID)
	require.Equal(t, super.ID, *b.reload(t, p2).TelegramChatID)
	require.Nil(t, messageID(t, b.g, taskID), "сообщения старой группы в новой не редактируются")

	b.service(&models.Update{Message: &models.Message{ID: 2, Chat: super, From: &models.User{ID: mTG}, NewChatTitle: "Новая"}})
	require.Equal(t, "Новая", *b.reload(t, p1).TelegramChatTitle)
	require.Empty(t, b.messages(t), "на служебные сообщения бот не отвечает")

	// После миграции команды работают в супергруппе.
	b.say(mTG, super, "/unbind "+p1.Key)
	require.Equal(t, []string{"Группа отключена от проекта " + p1.Key + " «Первый»"}, b.replies(t))
}

// Приёмка #50: «после миграции в супергруппу сообщения продолжают приходить» — сообщение в старую
// группу Telegram отклоняет с migrate_to_chat_id, отправитель повторяет его в новую.
func TestDeliverFollowsGroupMigration(t *testing.T) {
	e := newEnv(t)
	d := telegram.NewDeliverer(e.api, e.rdb, e.out)
	e.tg.Respond("sendMessage", `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1009876}}`)

	require.NoError(t, e.deliver(t, d, telegram.Message{Op: telegram.OpSend, ChatID: -555, ReplyTo: 12, Text: "карточка"}))
	msgs := e.messages(t)
	require.Len(t, msgs, 1)
	require.Equal(t, telegram.Message{Op: telegram.OpSend, ChatID: -1009876, Text: "карточка"}, msgs[0])
	require.NoError(t, e.deliver(t, d, msgs[0]))
	calls := e.tg.Calls("sendMessage")
	require.Len(t, calls, 2)
	require.Equal(t, "-1009876", calls[1].Params["chat_id"])
}

// Обработчик служебных обновлений не зарегистрирован — обновление пропускается.
func TestChatUpdateWithoutHandler(t *testing.T) {
	e := newEnv(t)
	p := telegram.NewProcessor(e.api, telegram.NewRouter("TaskBot"), e.out)
	p.Handle(context.Background(), &models.Update{ID: 1, Message: &models.Message{Chat: models.Chat{ID: -1, Type: models.ChatTypeGroup},
		MigrateToChatID: -2}}, time.Now())
	require.Empty(t, e.messages(t))
}

// newTask — задача проекта с номером сообщения в группе; возвращает id.
func newTask(t *testing.T, g *gorm.DB, p *project.Project, reporter *user.User, messageID int) uuid.UUID {
	t.Helper()
	n, err := project.NextTaskNumber(g, p.ID)
	require.NoError(t, err)
	id := uuid.New()
	require.NoError(t, g.Exec(`INSERT INTO tasks (id, project_id, number, key, title, reporter_id, position, telegram_message_id)
		VALUES (?, ?, ?, ?, 'Задача', ?, 1, ?)`, id, p.ID, n, p.Key+"-"+strconv.Itoa(n), reporter.ID, messageID).Error)
	return id
}

func messageID(t *testing.T, g *gorm.DB, taskID uuid.UUID) *int64 {
	t.Helper()
	var v struct{ TelegramMessageID *int64 }
	res := g.Raw(`SELECT telegram_message_id FROM tasks WHERE id = ?`, taskID).Scan(&v)
	require.NoError(t, res.Error)
	require.EqualValues(t, 1, res.RowsAffected)
	return v.TelegramMessageID
}
