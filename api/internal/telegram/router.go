package telegram

import (
	"context"
	"strings"
	"sync"
	"unicode"

	"github.com/go-telegram/bot/models"

	"github.com/jack5505/taskflow/api/internal/user"
)

// HandlerFunc обрабатывает команду или нажатие кнопки. Ошибка пишется в лог, пользователь
// получает общий ответ internal_error.
type HandlerFunc func(ctx context.Context, r *Request) error

// Middleware оборачивает обработчик (например, определяет пользователя по telegram_user_id).
type Middleware func(next HandlerFunc) HandlerFunc

// Request — разобранное обновление: команда или нажатие кнопки, и способы ответить.
type Request struct {
	Update *models.Update
	Chat   models.Chat // чат, где написана команда или нажата кнопка
	From   models.User // кто написал или нажал
	// MessageID — сообщение с командой; для кнопки — сообщение, под которым она нажата.
	MessageID int

	Command string // имя команды без «/» и «@бот», в нижнем регистре; пусто для кнопки и текста
	Args    string // текст после команды; для обычного текста (Router.Text) — весь текст

	Action string // действие кнопки: часть callback_data до «:» (§8.8: take:<task_id>)
	Data   string // часть callback_data после «:»

	// User — пользователь системы, привязанный к From (§8.1); nil — не привязан. Заполняет
	// middleware RequireLinked.
	User *user.User

	callbackID string
	service    bool // служебное обновление чата (ChatUpdate): отвечать в чат некому
	out        *Queue
	mu         sync.Mutex
	answer     *callbackAnswer
}

type callbackAnswer struct {
	text  string
	alert bool
}

// Private — личный чат с ботом.
func (r *Request) Private() bool { return r.Chat.Type == models.ChatTypePrivate }

// IsCallback — нажатие кнопки, а не команда.
func (r *Request) IsCallback() bool { return r.callbackID != "" }

// IsText — обычный текст в личном чате, а не команда и не кнопка (Router.Text).
func (r *Request) IsText() bool { return r.callbackID == "" && r.Command == "" }

// Reply отвечает в тот же чат; в группе — ответом на сообщение с командой.
func (r *Request) Reply(ctx context.Context, text string) error {
	return r.out.Send(ctx, r.reply(text), 0)
}

// ReplyHTML — Reply с разметкой HTML Telegram.
func (r *Request) ReplyHTML(ctx context.Context, text string) error {
	m := r.reply(text)
	m.HTML = true
	return r.out.Send(ctx, m, 0)
}

// ReplyTemp — ответ, который в группе бот удаляет через 30 секунд (§8.1, §8.4); в личном чате
// это обычный ответ.
func (r *Request) ReplyTemp(ctx context.Context, text string) error {
	m := r.reply(text)
	if !r.Private() {
		m.DeleteAfter = int(TempReplyTTL.Seconds())
	}
	return r.out.Send(ctx, m, 0)
}

// Answer — текст подтверждения нажатия кнопки (alert — всплывающее окно). Уходит в
// answerCallbackQuery после обработчика; для команды — временным ответом в чат.
func (r *Request) Answer(ctx context.Context, text string, alert bool) error {
	if !r.IsCallback() {
		return r.ReplyTemp(ctx, text)
	}
	r.mu.Lock()
	r.answer = &callbackAnswer{text: text, alert: alert}
	r.mu.Unlock()
	return nil
}

func (r *Request) takeAnswer() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.answer == nil {
		return "", false
	}
	return r.answer.text, r.answer.alert
}

func (r *Request) reply(text string) Message {
	m := Message{Op: OpSend, ChatID: r.Chat.ID, Text: text}
	if !r.Private() {
		m.ReplyTo = r.MessageID
	}
	return m
}

// Router раздаёт команды и нажатия кнопок обработчикам. Домены регистрируют свои команды при
// старте воркера.
type Router struct {
	botUsername string
	commands    map[string]HandlerFunc
	callbacks   map[string]HandlerFunc
	text        HandlerFunc
	middlewares []Middleware
	// chatUpdate — служебные обновления чата (§8.2): бота удалили из группы, группа стала
	// супергруппой, группу переименовали. Без middleware: отправитель тут не важен.
	chatUpdate HandlerFunc
}

// NewRouter — роутер бота с именем botUsername: команды вида /take@другой_бот в группе
// адресованы не нам и пропускаются.
func NewRouter(botUsername string) *Router {
	return &Router{botUsername: strings.TrimPrefix(botUsername, "@"),
		commands: map[string]HandlerFunc{}, callbacks: map[string]HandlerFunc{}}
}

// Command регистрирует команду name (без «/»).
func (rt *Router) Command(name string, h HandlerFunc) { rt.commands[strings.ToLower(name)] = h }

// Callback регистрирует действие кнопки: callback_data вида "<action>:<данные>".
func (rt *Router) Callback(action string, h HandlerFunc) { rt.callbacks[action] = h }

// ChatUpdate регистрирует обработчик служебных обновлений чата: my_chat_member и сообщений
// с migrate_to_chat_id, migrate_from_chat_id, new_chat_title. Middleware к нему не применяются.
func (rt *Router) ChatUpdate(h HandlerFunc) { rt.chatUpdate = h }

// Text регистрирует обработчик обычного текста (не команды) в личном чате: ответ на вопрос бота,
// например причина возврата на доработку (§8.7). Текст в группах бот не читает (§8.2).
func (rt *Router) Text(h HandlerFunc) { rt.text = h }

// Use добавляет middleware ко всем обработчикам; первая добавленная — внешняя.
func (rt *Router) Use(mw Middleware) { rt.middlewares = append(rt.middlewares, mw) }

func (rt *Router) wrap(h HandlerFunc) HandlerFunc {
	for i := len(rt.middlewares) - 1; i >= 0; i-- {
		h = rt.middlewares[i](h)
	}
	return h
}

// parseCommand разбирает «/take@bot TM-42» → ("take", "TM-42", true). ok=false — не команда
// или команда другому боту.
func (rt *Router) parseCommand(text string) (name, args string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, rest := text, ""
	if i := strings.IndexFunc(text, unicode.IsSpace); i >= 0 {
		head, rest = text[:i], text[i:]
	}
	head = strings.TrimPrefix(head, "/")
	if cmd, target, found := strings.Cut(head, "@"); found {
		if rt.botUsername == "" || !strings.EqualFold(target, rt.botUsername) {
			return "", "", false
		}
		head = cmd
	}
	if head == "" {
		return "", "", false
	}
	return strings.ToLower(head), strings.TrimSpace(rest), true
}

// route находит обработчик обновления. nil — обновление боту не интересно.
func (rt *Router) route(upd *models.Update, out *Queue) (*Request, HandlerFunc) {
	switch {
	case upd.MyChatMember != nil && rt.chatUpdate != nil:
		cm := upd.MyChatMember
		return &Request{Update: upd, Chat: cm.Chat, From: cm.From, out: out, service: true}, rt.chatUpdate
	case upd.Message != nil && isChatService(upd.Message) && rt.chatUpdate != nil:
		msg := upd.Message
		r := &Request{Update: upd, Chat: msg.Chat, MessageID: msg.ID, out: out, service: true}
		if msg.From != nil {
			r.From = *msg.From
		}
		return r, rt.chatUpdate
	case upd.Message != nil && upd.Message.From != nil:
		msg := upd.Message
		name, args, ok := rt.parseCommand(msg.Text)
		if !ok {
			if rt.text == nil || msg.Chat.Type != models.ChatTypePrivate || strings.TrimSpace(msg.Text) == "" {
				return nil, nil
			}
			return &Request{Update: upd, Chat: msg.Chat, From: *msg.From, MessageID: msg.ID,
				Args: strings.TrimSpace(msg.Text), out: out}, rt.wrap(rt.text)
		}
		h := rt.commands[name]
		if h == nil {
			h = rt.commands[UnknownCommand]
		}
		if h == nil {
			return nil, nil
		}
		return &Request{Update: upd, Chat: msg.Chat, From: *msg.From, MessageID: msg.ID,
			Command: name, Args: args, out: out}, rt.wrap(h)
	case upd.CallbackQuery != nil:
		cq := upd.CallbackQuery
		r := &Request{Update: upd, From: cq.From, callbackID: cq.ID, out: out}
		if m := cq.Message.Message; m != nil {
			r.Chat, r.MessageID = m.Chat, m.ID
		} else if m := cq.Message.InaccessibleMessage; m != nil {
			r.Chat, r.MessageID = m.Chat, m.MessageID
		}
		r.Action, r.Data, _ = strings.Cut(cq.Data, ":")
		h := rt.callbacks[r.Action]
		if h == nil {
			// Кнопка старой версии бота: подтверждаем нажатие, чтобы не крутился индикатор.
			return r, func(context.Context, *Request) error { return nil }
		}
		return r, rt.wrap(h)
	}
	return nil, nil
}

// isChatService — служебное сообщение о самом чате, которое нужно боту (§8.2).
func isChatService(m *models.Message) bool {
	return m.MigrateToChatID != 0 || m.MigrateFromChatID != 0 || m.NewChatTitle != ""
}

// UnknownCommand — имя для Command: обработчик команд, у которых нет своего (§8.5:
// «Неизвестная команда» в личном чате).
const UnknownCommand = "*"
