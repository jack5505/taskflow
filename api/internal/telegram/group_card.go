package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/sprint"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// KindCard — карточка задачи в группе проекта (§8.3): Message.Ref — id задачи.
const KindCard = "card"

// CardDelay — сколько ждать перед правкой карточки: события одной операции (взятие задачи —
// это назначение и переход) и правки за одну секунду дают одну правку по последнему состоянию.
const CardDelay = time.Second

// ActionTake — кнопка «Взять в работу» (§8.4, §8.8: take:<task_id>); обработчик — в командах задач.
const ActionTake = "take"

// cardFields — поля задачи, которые видны в карточке; правка других полей (описание, метки,
// порядок) карточку не трогает и не тратит лимит сообщений группы.
var cardFields = []string{"title", "type", "priority", "status", "assignee_id", "sprint_id", "estimate_minutes", "due_date"}

// Cards — карточки задач в группах проектов (§8.3): публикация при создании задачи и правка при
// изменениях. Сообщение собирается при отправке по текущему состоянию задачи, поэтому порядок
// доставки заданий не важен: последняя правка показывает последнее состояние.
type Cards struct {
	db      *gorm.DB
	out     *Queue
	baseURL string
	now     func() time.Time
}

func NewCards(db *gorm.DB, out *Queue, baseURL string) *Cards {
	return &Cards{db: db, out: out, baseURL: strings.TrimRight(baseURL, "/"), now: time.Now}
}

// RegisterCards подключает карточки задач: сборку сообщения KindCard в отправителе и обработчики
// событий задач §9.4 (task.created — публикация, остальные — правка).
func RegisterCards(rt *Router, d *events.Dispatcher, c *Cards) {
	rt.Kind(KindCard, KindHooks{Render: c.render, Sent: c.sent, ReplaceOnEditError: true})
	d.On(events.TaskCreated, "telegram_card", c.onEvent(true))
	for _, typ := range []string{events.TaskUpdated, events.TaskTransitioned, events.TaskAssigned, events.TaskDeleted, events.TaskRestored} {
		d.On(typ, "telegram_card", c.onEvent(false))
	}
	// Деактивация освобождает задачи без событий по каждой (§3.1) — карточки тоже правятся.
	d.On(events.UserDeactivated, "telegram_card", c.onDeactivated)
}

// onEvent — событие задачи: created=true — опубликовать карточку, иначе — поправить её.
func (c *Cards) onEvent(created bool) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p struct {
			TaskID uuid.UUID `json:"task_id"`
			Fields []string  `json:"fields"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("telegram: %s payload: %w", env.Type, err)
		}
		if created {
			// id без секунды: повтор события не опубликует вторую карточку.
			return c.out.SendOnce(ctx, Message{Op: OpSend, Kind: KindCard, Ref: p.TaskID.String()}, 0, "tg-card-new:"+p.TaskID.String())
		}
		if env.Type == events.TaskUpdated && len(p.Fields) > 0 && !slices.ContainsFunc(p.Fields, func(f string) bool {
			return slices.Contains(cardFields, f)
		}) {
			return nil
		}
		return c.Edit(ctx, p.TaskID)
	}
}

func (c *Cards) onDeactivated(ctx context.Context, env events.Envelope) error {
	var p struct {
		Released []string `json:"released_tasks"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return fmt.Errorf("telegram: %s payload: %w", env.Type, err)
	}
	if len(p.Released) == 0 {
		return nil
	}
	ids, err := task.IDsWithMessage(c.db.WithContext(ctx), p.Released)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := c.Edit(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Edit ставит правку карточки задачи через CardDelay; события той же секунды правку не
// дублируют, а задание выполняется уже после её конца — по последнему состоянию.
func (c *Cards) Edit(ctx context.Context, taskID uuid.UUID) error {
	return c.out.SendOnce(ctx, Message{Op: OpEdit, Kind: KindCard, Ref: taskID.String()}, CardDelay,
		Bucket("tg-card:"+taskID.String(), c.now()))
}

// groupTask — задача (и удалённая) с группой проекта; имена — после loadNames.
type groupTask struct {
	task.Snapshot
	// ChatID — группа проекта; у удалённого проекта nil.
	ChatID           *int64
	Settings         events.JSON
	Reporter         string
	Assignee         *string
	AssigneeUsername *string
}

// parseRef — id задачи из ссылки сообщения очереди (Message.Ref).
func parseRef(ref string) (uuid.UUID, error) {
	id, err := uuid.Parse(ref)
	if err != nil {
		return uuid.Nil, fmt.Errorf("telegram: task ref %q: %w", ref, err)
	}
	return id, nil
}

// loadGroupTask — задача id и группа её проекта; нет задачи — nil.
func loadGroupTask(db *gorm.DB, id uuid.UUID) (*groupTask, error) {
	t, err := task.SnapshotWithDeleted(db, id)
	if err != nil || t == nil {
		return nil, err
	}
	chat, err := project.ChatOf(db, t.ProjectID)
	if err != nil || chat == nil {
		return nil, err
	}
	return &groupTask{Snapshot: *t, ChatID: chat.ChatID, Settings: chat.Settings}, nil
}

// loadNames дописывает имена автора и исполнителя; автора нет — false.
func (r *groupTask) loadNames(db *gorm.DB) (bool, error) {
	ids := []uuid.UUID{r.ReporterID}
	if r.AssigneeID != nil {
		ids = append(ids, *r.AssigneeID)
	}
	users, err := user.Names(db, ids...)
	if err != nil {
		return false, err
	}
	if r.AssigneeID != nil {
		if a, ok := users[*r.AssigneeID]; ok {
			r.Assignee, r.AssigneeUsername = &a.DisplayName, a.TelegramUsername
		}
	}
	reporter, ok := users[r.ReporterID]
	r.Reporter = reporter.DisplayName
	return ok, nil
}

// readSnapshot выполняет чтения fn в одной read-only транзакции REPEATABLE READ: задача, проект
// и имена — из одного состояния базы, как раньше одним запросом с JOIN.
func readSnapshot(db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.Transaction(fn, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

// cardRow — задача со всем, что печатается в карточке.
type cardRow struct {
	groupTask
	Sprint *string
}

// load — карточка задачи taskID; нет задачи, проекта или автора — nil.
func (c *Cards) load(ctx context.Context, taskID string) (*cardRow, error) {
	id, err := parseRef(taskID)
	if err != nil {
		return nil, err
	}
	var r *cardRow
	err = readSnapshot(c.db.WithContext(ctx), func(tx *gorm.DB) error {
		t, err := loadGroupTask(tx, id)
		if err != nil || t == nil {
			return err
		}
		if ok, err := t.loadNames(tx); err != nil || !ok {
			return err
		}
		r = &cardRow{groupTask: *t}
		if t.SprintID != nil {
			name, err := sprint.Name(tx, *t.SprintID)
			if err != nil {
				return err
			}
			if name != "" {
				r.Sprint = &name
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// render собирает карточку при отправке (KindHooks.Render). Группа отключена — отправлять
// нечего. Публикация: только если включено событие «Создана задача» (§8.6) и карточки ещё нет.
// Правка: только если карточка есть; удалённая задача — «Задача удалена» без кнопок (§4).
func (c *Cards) render(ctx context.Context, m *Message) (bool, error) {
	r, err := c.load(ctx, m.Ref)
	if err != nil || r == nil || r.ChatID == nil {
		return false, err
	}
	m.ChatID, m.HTML = *r.ChatID, true
	switch {
	case m.Op == OpSend && m.Republish:
		// /task (§8.5): новая карточка становится текущей; прежнюю (Replaces) удалит sent.
		if r.Deleted {
			return false, nil
		}
	case m.Op == OpSend && m.Replaces != 0:
		if r.TelegramMessageID == nil || *r.TelegramMessageID != m.Replaces || r.Deleted {
			return false, nil // карточку уже заменили или задачу удалили
		}
	case m.Op == OpSend:
		if r.TelegramMessageID != nil || r.Deleted || !project.EffectiveNotifications(r.Settings)[project.NotifyTaskCreated] {
			return false, nil
		}
	default:
		if r.TelegramMessageID == nil {
			return false, nil
		}
		m.MessageID = *r.TelegramMessageID
	}
	m.Text, m.Keyboard = c.content(r)
	return true, nil
}

// Show — карточка задачи для показа в любом чате (/task в личном чате): без запоминания
// сообщения; ChatID заполняет вызывающий. Задачи нет — nil.
func (c *Cards) Show(ctx context.Context, taskID string) (*Message, error) {
	r, err := c.load(ctx, taskID)
	if err != nil || r == nil {
		return nil, err
	}
	m := &Message{Op: OpSend, HTML: true}
	m.Text, m.Keyboard = c.content(r)
	return m, nil
}

// content — текст и кнопки карточки; удалённая задача — «Задача удалена» без кнопок (§4).
func (c *Cards) content(r *cardRow) (string, *models.InlineKeyboardMarkup) {
	if r.Deleted {
		return Text("card_deleted", r), nil
	}
	return Text("card", c.view(r)), c.keyboard(r)
}

// sent — после отправки (KindHooks.Sent): новая карточка запоминается в задаче
// (telegram_message_id, §8.3); если карточка за время отправки устарела (задачу изменили),
// ставится ещё одна правка. Изменения, которых в карточке не видно, правку не вызывают.
func (c *Cards) sent(ctx context.Context, m Message, msg *models.Message) error {
	if msg != nil {
		id, err := parseRef(m.Ref)
		if err != nil {
			return err
		}
		if err := task.SetMessage(c.db.WithContext(ctx), id, msg.ID); err != nil {
			return err
		}
		if m.Replaces != 0 {
			// Прежняя карточка больше не правится — убираем её, чтобы не висела с устаревшим
			// статусом и кнопкой. Сообщение старше 48 часов Telegram удалить не даст — это не ошибка.
			if err := c.out.Send(ctx, Message{Op: OpDelete, ChatID: m.ChatID, MessageID: m.Replaces}, 0); err != nil {
				return err
			}
		}
	}
	r, err := c.load(ctx, m.Ref)
	if err != nil || r == nil {
		return err
	}
	text, kb := c.content(r)
	if text == m.Text && sameKeyboard(kb, m.Keyboard) {
		return nil
	}
	return c.Edit(ctx, r.ID)
}

func sameKeyboard(a, b *models.InlineKeyboardMarkup) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// cardView — данные шаблона карточки (texts.go).
type cardView struct {
	Key, Title, Type, Priority, Status, Reporter, Assignee string
	Details                                                []string // «Спринт: …», «Оценка: …», «Срок: …» — только заполненные
}

func (c *Cards) view(r *cardRow) cardView {
	v := cardView{Key: r.Key, Title: r.Title, Type: typeLabels[r.Type], Priority: priorityLabels[r.Priority],
		Status: statusLabels[r.Status], Reporter: r.Reporter}
	v.Assignee = mention(r.Assignee, r.AssigneeUsername)
	if r.Sprint != nil {
		v.Details = append(v.Details, "Спринт: "+*r.Sprint)
	}
	if r.EstimateMinutes != nil {
		v.Details = append(v.Details, "Оценка: "+duration.Format(*r.EstimateMinutes))
	}
	if r.DueDate != nil {
		v.Details = append(v.Details, "Срок: "+r.DueDate.Format("02.01.2006"))
	}
	return v
}

// keyboard — «Взять в работу» пока задача в todo и без исполнителя (§8.3), «Открыть» — карточка в вебе.
func (c *Cards) keyboard(r *cardRow) *models.InlineKeyboardMarkup {
	var row []models.InlineKeyboardButton
	if r.Status == "todo" && r.AssigneeID == nil {
		row = append(row, models.InlineKeyboardButton{Text: Text("btn_take", nil), CallbackData: ActionTake + ":" + r.ID.String()})
	}
	row = append(row, models.InlineKeyboardButton{Text: Text("btn_open", nil), URL: c.baseURL + "/tasks/" + r.Key})
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
}
