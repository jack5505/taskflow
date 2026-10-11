package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
)

// KindTaskEvent — сообщение в группу о событии задачи (§8.6): Message.Ref — id задачи.
const KindTaskEvent = "task_event"

// EventDelay — окно объединения: если за секунду по задаче случилось несколько событий, в группу
// уходит одно сообщение по последнему (§8.6).
const EventDelay = time.Second

// lastEventTTL — сколько хранить последнее неотправленное событие задачи (с запасом на очередь).
const lastEventTTL = 10 * time.Minute

// maxSprintTasks — сколько свободных задач с кнопками «Взять» перечислять при запуске спринта
// (как /tasks, §8.5).
const maxSprintTasks = 20

// taskEvent — событие задачи, о котором пишем в группу. Kind — шаблон ev_<kind> в texts.go
// и (через eventSetting) настройка проекта §8.6.
type taskEvent struct {
	Kind    string    `json:"kind"`
	By      uuid.UUID `json:"by"`
	Comment string    `json:"comment,omitempty"`
}

// eventSetting — событие → ключ notification_settings проекта (§8.6).
var eventSetting = map[string]string{
	"taken": project.NotifyTaskTaken, "assigned": project.NotifyTaskTaken, "returned": project.NotifyTaskReturned,
	"review": project.NotifyTaskReview, "rework": project.NotifyTaskRework, "accepted": project.NotifyTaskAccepted,
	"cancelled": project.NotifyTaskCancelled,
}

// transitionEvent — переход §4 → событие §8.6; переходы, о которых группе не пишем
// (переоткрыть, вернуть в бэклог), — "".
func transitionEvent(from, to string) string {
	switch {
	case from == "todo" && to == "in_progress":
		return "taken"
	case from == "in_progress" && to == "todo":
		return "returned"
	case from == "in_progress" && to == "review":
		return "review"
	case from == "review" && to == "in_progress":
		return "rework"
	case from == "review" && to == "done":
		return "accepted"
	case to == "cancelled":
		return "cancelled"
	}
	return ""
}

// GroupEvents — сообщения бота в группу проекта по событиям (§8.6). События из веба и из бота
// приходят одними и теми же событиями §9.4, поэтому и публикуются одинаково.
type GroupEvents struct {
	db  *gorm.DB
	rdb *redis.Client
	out *Queue
	now func() time.Time
}

func NewGroupEvents(db *gorm.DB, rdb *redis.Client, out *Queue) *GroupEvents {
	return &GroupEvents{db: db, rdb: rdb, out: out, now: time.Now}
}

// RegisterGroupEvents подключает сообщения о событиях: сборку KindTaskEvent в отправителе и
// обработчики событий задач, комментариев и спринтов.
func RegisterGroupEvents(rt *Router, d *events.Dispatcher, ge *GroupEvents) {
	rt.Kind(KindTaskEvent, KindHooks{Render: ge.render, Sent: ge.sent})
	d.On(events.TaskTransitioned, "telegram_group", ge.onTransitioned)
	d.On(events.TaskAssigned, "telegram_group", ge.onAssigned)
	d.On(events.CommentCreated, "telegram_group", ge.onComment)
	d.On(events.SprintStarted, "telegram_group", ge.onSprintStarted)
	d.On(events.SprintClosed, "telegram_group", ge.onSprintClosed)
}

func decodePayload(env events.Envelope, p any) error {
	if err := json.Unmarshal(env.Payload, p); err != nil {
		return fmt.Errorf("telegram: %s payload: %w", env.Type, err)
	}
	return nil
}

func (ge *GroupEvents) onTransitioned(ctx context.Context, env events.Envelope) error {
	var p struct {
		TaskID  uuid.UUID `json:"task_id"`
		From    string    `json:"from"`
		To      string    `json:"to"`
		By      uuid.UUID `json:"by"`
		Comment string    `json:"comment"`
	}
	if err := decodePayload(env, &p); err != nil {
		return err
	}
	kind := transitionEvent(p.From, p.To)
	if kind == "" {
		return nil
	}
	return ge.queue(ctx, p.TaskID, taskEvent{Kind: kind, By: p.By, Comment: p.Comment})
}

// onAssigned — назначение менеджером (§8.4): «📌 … исполнитель … — назначение: …». Взятие себе
// task.assigned не публикует — о нём пишет переход.
func (ge *GroupEvents) onAssigned(ctx context.Context, env events.Envelope) error {
	var p struct {
		TaskID uuid.UUID `json:"task_id"`
		By     uuid.UUID `json:"by"`
	}
	if err := decodePayload(env, &p); err != nil {
		return err
	}
	return ge.queue(ctx, p.TaskID, taskEvent{Kind: "assigned", By: p.By})
}

func lastEventKey(taskID string) string { return "tg:event:" + taskID }

// queue запоминает событие как последнее по задаче и ставит отправку через EventDelay; пока
// отправка ждёт, следующее событие только заменяет последнее.
func (ge *GroupEvents) queue(ctx context.Context, taskID uuid.UUID, ev taskEvent) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("telegram: event: %w", err)
	}
	if err := ge.rdb.Set(ctx, lastEventKey(taskID.String()), raw, lastEventTTL).Err(); err != nil {
		return fmt.Errorf("telegram: last event: %w", err)
	}
	return ge.schedule(ctx, taskID.String())
}

// schedule ставит отправку последнего события задачи через EventDelay; события той же секунды
// второй отправки не ставят.
func (ge *GroupEvents) schedule(ctx context.Context, taskID string) error {
	return ge.out.SendOnce(ctx, Message{Op: OpSend, Kind: KindTaskEvent, Ref: taskID}, EventDelay, Bucket("tg-event:"+taskID, ge.now()))
}

// eventRow — задача и группа для сообщения о событии.
type eventRow struct {
	Key               string
	Title             string
	Deleted           bool
	TelegramMessageID *int
	SpentMinutes      int
	EstimateMinutes   *int
	ChatID            *int64
	Settings          events.JSON
	Assignee          *string
	AssigneeUsername  *string
	Actor             string
}

// render собирает сообщение по последнему событию задачи (KindHooks.Render). Собранное раньше
// (повтор после ошибки Telegram или ожидания лимита) не пересобирается: последнее событие уже
// забрано. Нет события, группы, задача удалена или событие выключено в проекте — не отправляем.
func (ge *GroupEvents) render(ctx context.Context, m *Message) (bool, error) {
	if m.Text != "" {
		return true, nil
	}
	raw, err := ge.rdb.GetDel(ctx, lastEventKey(m.Ref)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("telegram: last event: %w", err)
	}
	var ev taskEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return false, fmt.Errorf("telegram: last event: %w", err)
	}
	var r eventRow
	res := ge.db.WithContext(ctx).Raw(`SELECT t.key, t.title, t.deleted_at IS NOT NULL AS deleted, t.telegram_message_id,
			t.spent_minutes, t.estimate_minutes,
			CASE WHEN p.deleted_at IS NULL THEN p.telegram_chat_id END AS chat_id, p.notification_settings AS settings,
			a.display_name AS assignee, a.telegram_username AS assignee_username, COALESCE(b.display_name, '') AS actor
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		LEFT JOIN users a ON a.id = t.assignee_id
		LEFT JOIN users b ON b.id = ?
		WHERE t.id = ?`, ev.By, m.Ref).Scan(&r)
	if res.Error != nil {
		// Событие уже забрано — вернём его, чтобы повтор задания его нашёл.
		_ = ge.rdb.SetNX(context.WithoutCancel(ctx), lastEventKey(m.Ref), raw, lastEventTTL).Err()
		return false, fmt.Errorf("telegram: event task: %w", res.Error)
	}
	if res.RowsAffected == 0 || r.ChatID == nil || r.Deleted || !project.EffectiveNotifications(r.Settings)[eventSetting[ev.Kind]] {
		return false, nil
	}
	data := map[string]any{"Key": r.Key, "Title": r.Title, "Actor": r.Actor, "Comment": ev.Comment,
		"Assignee": mention(r.Assignee, r.AssigneeUsername), "Spent": spentText(r.SpentMinutes, r.EstimateMinutes)}
	m.ChatID, m.Text, m.HTML = *r.ChatID, Text("ev_"+ev.Kind, data), false
	if r.TelegramMessageID != nil {
		m.ReplyTo = *r.TelegramMessageID // ответом на карточку задачи (§8.6)
	}
	return true, nil
}

// sent — пока сообщение собиралось и отправлялось, могло прийти новое событие той же секунды,
// а его задание не поставилось: тогда ставим отправку ещё раз.
func (ge *GroupEvents) sent(ctx context.Context, m Message, _ *models.Message) error {
	n, err := ge.rdb.Exists(ctx, lastEventKey(m.Ref)).Result()
	if err != nil || n == 0 {
		return err
	}
	return ge.schedule(ctx, m.Ref)
}

// onComment — «💬 Иван Петров к TM-42: <первые 200 символов>» (§8.6, по умолчанию выключено).
// Комментарий — не смена состояния задачи, с переходами не объединяется.
func (ge *GroupEvents) onComment(ctx context.Context, env events.Envelope) error {
	var p struct {
		TaskID   uuid.UUID `json:"task_id"`
		AuthorID uuid.UUID `json:"author_id"`
		Key      string    `json:"key"`
		Body     string    `json:"body"`
	}
	if err := decodePayload(env, &p); err != nil {
		return err
	}
	var r struct {
		ChatID            *int64
		Settings          events.JSON
		TelegramMessageID *int
		Author            string
	}
	res := ge.db.WithContext(ctx).Raw(`SELECT CASE WHEN p.deleted_at IS NULL AND t.deleted_at IS NULL THEN p.telegram_chat_id END AS chat_id,
			p.notification_settings AS settings, t.telegram_message_id, u.display_name AS author
		FROM tasks t JOIN projects p ON p.id = t.project_id JOIN users u ON u.id = ?
		WHERE t.id = ?`, p.AuthorID, p.TaskID).Scan(&r)
	if res.Error != nil {
		return fmt.Errorf("telegram: comment task: %w", res.Error)
	}
	if res.RowsAffected == 0 || r.ChatID == nil || !project.EffectiveNotifications(r.Settings)[project.NotifyCommentCreated] {
		return nil
	}
	m := Message{Op: OpSend, ChatID: *r.ChatID, Text: Text("ev_comment", map[string]any{"Author": r.Author, "Key": p.Key, "Body": p.Body})}
	if r.TelegramMessageID != nil {
		m.ReplyTo = *r.TelegramMessageID
	}
	return ge.out.Send(ctx, m, 0)
}

// groupChat — группа проекта, если она есть и событие setting включено.
func (ge *GroupEvents) groupChat(ctx context.Context, projectID uuid.UUID, setting string) (int64, bool, error) {
	var r struct {
		ChatID   *int64
		Settings events.JSON
	}
	res := ge.db.WithContext(ctx).Raw(`SELECT telegram_chat_id AS chat_id, notification_settings AS settings
		FROM projects WHERE id = ? AND deleted_at IS NULL`, projectID).Scan(&r)
	if res.Error != nil {
		return 0, false, fmt.Errorf("telegram: project chat: %w", res.Error)
	}
	if res.RowsAffected == 0 || r.ChatID == nil || !project.EffectiveNotifications(r.Settings)[setting] {
		return 0, false, nil
	}
	return *r.ChatID, true, nil
}

// onSprintStarted — «🚀 Спринт 7 запущен: 13.10–24.10.2026, 18 задач, оценка 64h» и свободные
// задачи спринта с кнопками «Взять» (§8.6).
func (ge *GroupEvents) onSprintStarted(ctx context.Context, env events.Envelope) error {
	var p struct {
		SprintID        uuid.UUID `json:"sprint_id"`
		ProjectID       uuid.UUID `json:"project_id"`
		Name            string    `json:"name"`
		StartDate       string    `json:"start_date"`
		EndDate         string    `json:"end_date"`
		Tasks           int       `json:"tasks"`
		EstimateMinutes int       `json:"estimate_minutes"`
	}
	if err := decodePayload(env, &p); err != nil {
		return err
	}
	chatID, ok, err := ge.groupChat(ctx, p.ProjectID, project.NotifySprintStarted)
	if err != nil || !ok {
		return err
	}
	var free []struct {
		ID    uuid.UUID
		Key   string
		Title string
	}
	if err := ge.db.WithContext(ctx).Raw(`SELECT id, key, title FROM tasks
		WHERE sprint_id = ? AND status = 'todo' AND assignee_id IS NULL AND deleted_at IS NULL
		ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, position, key
		LIMIT ?`, p.SprintID, maxSprintTasks+1).Scan(&free).Error; err != nil {
		return fmt.Errorf("telegram: sprint free tasks: %w", err)
	}
	more := len(free) > maxSprintTasks
	if more {
		free = free[:maxSprintTasks]
	}
	type item struct{ Key, Title string }
	items := make([]item, len(free))
	var kb [][]models.InlineKeyboardButton
	for i, t := range free {
		items[i] = item{t.Key, t.Title}
		kb = append(kb, []models.InlineKeyboardButton{{Text: Text("button_take_key", t.Key), CallbackData: ActionTake + ":" + t.ID.String()}})
	}
	m := Message{Op: OpSend, ChatID: chatID, Text: Text("ev_sprint_started", map[string]any{
		"Name": p.Name, "Dates": dateRange(p.StartDate, p.EndDate), "Tasks": plural(p.Tasks, "задача", "задачи", "задач"),
		"Estimate": duration.Format(p.EstimateMinutes), "Free": items, "More": more,
	})}
	if len(kb) > 0 {
		m.Keyboard = &models.InlineKeyboardMarkup{InlineKeyboard: kb}
	}
	return ge.out.Send(ctx, m, 0)
}

// onSprintClosed — «🏁 Спринт 7 завершён: готово 15 из 18, перенесено 3, списано 58h из 64h» (§8.6).
func (ge *GroupEvents) onSprintClosed(ctx context.Context, env events.Envelope) error {
	var p struct {
		ProjectID       uuid.UUID `json:"project_id"`
		Name            string    `json:"name"`
		Done            int       `json:"done"`
		Total           int       `json:"total"`
		Moved           int       `json:"moved"`
		EstimateMinutes int       `json:"estimate_minutes"`
		SpentMinutes    int       `json:"spent_minutes"`
	}
	if err := decodePayload(env, &p); err != nil {
		return err
	}
	chatID, ok, err := ge.groupChat(ctx, p.ProjectID, project.NotifySprintClosed)
	if err != nil || !ok {
		return err
	}
	est := &p.EstimateMinutes
	if p.EstimateMinutes == 0 {
		est = nil
	}
	return ge.out.Send(ctx, Message{Op: OpSend, ChatID: chatID, Text: Text("ev_sprint_closed", map[string]any{
		"Name": p.Name, "Done": p.Done, "Total": p.Total, "Moved": p.Moved, "Spent": spentText(p.SpentMinutes, est),
	})}, 0)
}

// mention — «Иван Петров @ivan» (§8.3); исполнителя нет — «—».
func mention(name, username *string) string {
	if name == nil {
		return "—"
	}
	if username != nil && *username != "" {
		return *name + " @" + *username
	}
	return *name
}

// spentText — «2h 30m из 3h»; без оценки — «2h 30m».
func spentText(spent int, estimate *int) string {
	if estimate == nil {
		return duration.Format(spent)
	}
	return duration.Format(spent) + " из " + duration.Format(*estimate)
}

// dateRange — «13.10–24.10.2026»; через границу года — «28.12.2026–08.01.2027».
func dateRange(start, end string) string {
	s, err1 := time.Parse(time.DateOnly, firstN(start, 10))
	e, err2 := time.Parse(time.DateOnly, firstN(end, 10))
	if err1 != nil || err2 != nil {
		return ""
	}
	if s.Year() == e.Year() {
		return s.Format("02.01") + "–" + e.Format("02.01.2006")
	}
	return s.Format("02.01.2006") + "–" + e.Format("02.01.2006")
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// plural — «1 задача», «3 задачи», «18 задач».
func plural(n int, one, few, many string) string {
	form := many
	switch n10, n100 := n%10, n%100; {
	case n10 == 1 && n100 != 11:
		form = one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		form = few
	}
	return fmt.Sprintf("%d %s", n, form)
}
