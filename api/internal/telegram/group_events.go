package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// KindTaskEvent — сообщение в группу о событии задачи (§8.6): Message.Ref — id задачи.
const KindTaskEvent = "task_event"

// EventDelay — окно объединения: если за секунду по задаче случилось несколько событий, в группу
// уходит одно сообщение по последнему (§8.6).
const EventDelay = time.Second

// pendingTTL — сколько хранить неотправленные события задачи (с запасом на простой воркера).
const pendingTTL = 24 * time.Hour

// maxCommentRunes — сколько символов причины (возврат, отмена) печатать: комментарий бывает до
// 5000 символов (§3.6), а сообщение Telegram — до 4096.
const maxCommentRunes = 500

// maxSprintText — предел текста о запуске спринта: длинные названия задач не должны довести его
// до лимита Telegram 4096 символов (400 «message is too long» не повторяется).
const maxSprintText = 3500

// maxSprintTasks — сколько свободных задач с кнопками «Взять» перечислять при запуске спринта
// (как /tasks, §8.5).
const maxSprintTasks = 20

// taskEvent — событие задачи, о котором пишем в группу. Kind — шаблон ev_<kind> в texts.go
// и (через eventSetting) настройка проекта §8.6.
type taskEvent struct {
	Kind     string     `json:"kind"`
	By       uuid.UUID  `json:"by"`
	Comment  string     `json:"comment,omitempty"`
	To       string     `json:"to,omitempty"`       // переход: статус после него
	Assignee *uuid.UUID `json:"assignee,omitempty"` // назначение: кого назначили
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
	return ge.queue(ctx, p.TaskID, taskEvent{Kind: kind, By: p.By, Comment: truncate(p.Comment, maxCommentRunes), To: p.To})
}

// onAssigned — назначение менеджером (§8.4): «📌 … исполнитель … — назначение: …». Взятие себе
// task.assigned не публикует — о нём пишет переход.
func (ge *GroupEvents) onAssigned(ctx context.Context, env events.Envelope) error {
	var p struct {
		TaskID     uuid.UUID `json:"task_id"`
		By         uuid.UUID `json:"by"`
		AssigneeID uuid.UUID `json:"assignee_id"`
	}
	if err := decodePayload(env, &p); err != nil {
		return err
	}
	return ge.queue(ctx, p.TaskID, taskEvent{Kind: "assigned", By: p.By, Assignee: &p.AssigneeID})
}

func pendingKey(taskID string) string { return "tg:events:" + taskID }

// queue добавляет событие к неотправленным событиям задачи и ставит отправку через EventDelay;
// события той же секунды второй отправки не ставят.
func (ge *GroupEvents) queue(ctx context.Context, taskID uuid.UUID, ev taskEvent) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("telegram: event: %w", err)
	}
	key := pendingKey(taskID.String())
	pipe := ge.rdb.TxPipeline()
	pipe.RPush(ctx, key, raw)
	pipe.Expire(ctx, key, pendingTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("telegram: pending events: %w", err)
	}
	return ge.out.SendOnce(ctx, Message{Op: OpSend, Kind: KindTaskEvent, Ref: taskID.String()}, EventDelay,
		Bucket("tg-event:"+taskID.String(), ge.now()))
}

// render собирает одно сообщение по неотправленным событиям задачи (KindHooks.Render): берётся
// последнее событие, которое совпадает с текущим состоянием задачи (статусом или исполнителем), —
// обработчики событий могут выполниться не по порядку, а писать нужно о последнем состоянии
// (§8.6). События не удаляются до отправки (sent): повтор задания найдёт их снова. Собранное
// сообщение при повторе после ошибки Telegram не пересобирается. Нет группы, задача удалена,
// событие выключено в проекте или состояние уже другое — события забываются без сообщения.
func (ge *GroupEvents) render(ctx context.Context, m *Message) (bool, error) {
	if m.Text != "" {
		return true, nil
	}
	key := pendingKey(m.Ref)
	list, err := ge.rdb.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return false, fmt.Errorf("telegram: pending events: %w", err)
	}
	if len(list) == 0 {
		return false, nil
	}
	id, err := parseRef(m.Ref)
	if err != nil {
		return false, err
	}
	db := ge.db.WithContext(ctx)
	var r *groupTask
	if err := readSnapshot(db, func(tx *gorm.DB) error {
		var err error
		if r, err = loadGroupTask(tx, id); err != nil || r == nil {
			return err
		}
		if r.ChatID == nil || r.Deleted {
			return nil // сообщения не будет — имена не нужны
		}
		_, err = r.loadNames(tx)
		return err
	}); err != nil {
		return false, err
	}
	if r == nil {
		return false, ge.consume(ctx, key, len(list))
	}
	var ev *taskEvent
	for i := len(list) - 1; i >= 0 && ev == nil; i-- {
		var e taskEvent
		if json.Unmarshal([]byte(list[i]), &e) != nil {
			continue
		}
		if (e.To != "" && e.To == r.Status) || (e.Assignee != nil && r.AssigneeID != nil && *e.Assignee == *r.AssigneeID) {
			ev = &e
		}
	}
	if ev == nil || r.ChatID == nil || r.Deleted || !project.EffectiveNotifications(r.Settings)[eventSetting[ev.Kind]] {
		return false, ge.consume(ctx, key, len(list))
	}
	actor, _, err := user.DisplayName(db, ev.By)
	if err != nil {
		return false, err
	}
	data := map[string]any{"Key": r.Key, "Title": r.Title, "Actor": actor, "Comment": ev.Comment,
		"Assignee": mention(r.Assignee, r.AssigneeUsername), "Spent": spentText(r.SpentMinutes, r.EstimateMinutes)}
	m.ChatID, m.Text, m.HTML, m.Stamp = *r.ChatID, Text("ev_"+ev.Kind, data), false, len(list)
	if r.TelegramMessageID != nil {
		m.ReplyTo = *r.TelegramMessageID // ответом на карточку задачи (§8.6)
	}
	return true, nil
}

// sent — сообщение ушло: события, по которым оно собрано, забываются; пришедшие за это время
// остаются для своего задания.
func (ge *GroupEvents) sent(ctx context.Context, m Message, _ *models.Message) error {
	return ge.consume(ctx, pendingKey(m.Ref), m.Stamp)
}

func (ge *GroupEvents) consume(ctx context.Context, key string, n int) error {
	if err := ge.rdb.LTrim(ctx, key, int64(n), -1).Err(); err != nil {
		return fmt.Errorf("telegram: pending events: %w", err)
	}
	return nil
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
	db := ge.db.WithContext(ctx)
	r, err := loadGroupTask(db, p.TaskID)
	if err != nil {
		return err
	}
	if r == nil || r.Deleted || r.ChatID == nil || !project.EffectiveNotifications(r.Settings)[project.NotifyCommentCreated] {
		return nil
	}
	author, ok, err := user.DisplayName(db, p.AuthorID)
	if err != nil || !ok {
		return err
	}
	m := Message{Op: OpSend, ChatID: *r.ChatID, Text: Text("ev_comment", map[string]any{"Author": author, "Key": p.Key, "Body": p.Body})}
	if r.TelegramMessageID != nil {
		m.ReplyTo = *r.TelegramMessageID
	}
	return ge.out.Send(ctx, m, 0)
}

// groupChat — группа проекта, если она есть и событие setting включено.
func (ge *GroupEvents) groupChat(ctx context.Context, projectID uuid.UUID, setting string) (int64, bool, error) {
	r, err := project.ChatOf(ge.db.WithContext(ctx), projectID)
	if err != nil {
		return 0, false, err
	}
	if r == nil || r.ChatID == nil || !project.EffectiveNotifications(r.Settings)[setting] {
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
	free, err := task.FreeInSprints(ge.db.WithContext(ctx), []uuid.UUID{p.SprintID}, maxSprintTasks+1)
	if err != nil {
		return err
	}
	more := len(free) > maxSprintTasks
	if more {
		free = free[:maxSprintTasks]
	}
	type item struct{ Key, Title string }
	var items []item
	var kb [][]models.InlineKeyboardButton
	data := map[string]any{"Name": p.Name, "Dates": dateRange(p.StartDate, p.EndDate),
		"Tasks": plural(p.Tasks, "задача", "задачи", "задач"), "Estimate": duration.Format(p.EstimateMinutes), "More": more}
	for i, t := range free {
		next := append(items, item{t.Key, truncate(t.Title, 100)})
		data["Free"] = next
		if len(Text("ev_sprint_started", data)) > maxSprintText {
			data["Free"], data["More"] = items, true
			break
		}
		items = next
		kb = append(kb, []models.InlineKeyboardButton{{Text: Text("button_take_key", free[i].Key), CallbackData: ActionTake + ":" + t.ID.String()}})
	}
	m := Message{Op: OpSend, ChatID: chatID, Text: Text("ev_sprint_started", data)}
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

// truncate — не больше n символов, с «…» при обрезке.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
