package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Personal — личные уведомления в Telegram (§8.7) и кнопки «Принять» и «Вернуть» под
// уведомлением «отправлена на проверку». Уведомления строятся на тех же событиях outbox, что и
// колокольчик (§9.4), отдельными обработчиками диспетчера; получает их привязанный активный
// пользователь, у которого уведомление включено в профиле (notification_settings).
type Personal struct {
	db      *gorm.DB
	tasks   *task.Service
	rdb     *redis.Client
	out     *Queue
	baseURL string
}

// personalHandler — имя обработчиков диспетчера (учёт выполненных при повторе события).
const personalHandler = "telegram-personal"

// RegisterPersonal подключает личные уведомления к диспетчеру событий воркера и кнопки
// «Принять» и «Вернуть» (с ответом-причиной следующим сообщением) к роутеру бота.
func RegisterPersonal(rt *Router, d *events.Dispatcher, db *gorm.DB, tasks *task.Service, rdb *redis.Client,
	jobs *asynq.Client, baseURL string) *Personal {
	p := &Personal{db: db, tasks: tasks, rdb: rdb, out: NewQueue(jobs), baseURL: strings.TrimRight(baseURL, "/")}
	d.On(events.TaskAssigned, personalHandler, p.taskAssigned)
	d.On(events.TaskTransitioned, personalHandler, p.taskTransitioned)
	d.On(events.CommentCreated, personalHandler, p.commentCreated)
	d.On(events.TaskDeleted, personalHandler, p.taskDeleted)
	d.On(events.TimerAutoStopped, personalHandler, p.timerStopped)
	d.On(events.TimerTrimmed, personalHandler, p.worklogTrimmed)
	for _, typ := range []string{events.MemberAdded, events.MemberRemoved, events.MemberRoleChanged} {
		d.On(typ, personalHandler, p.membership)
	}
	rt.Callback(ActionAccept, p.acceptButton)
	rt.Callback(ActionRework, p.reworkButton)
	rt.Text(p.reworkReason)
	return p
}

// personalEvent — общие поля payload событий задачи (task.*, comment.created, timer.*).
type personalEvent struct {
	TaskID     uuid.UUID  `json:"task_id"`
	Key        string     `json:"key"`
	Title      string     `json:"title"`
	ProjectID  uuid.UUID  `json:"project_id"`
	By         uuid.UUID  `json:"by"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
	ReporterID uuid.UUID  `json:"reporter_id"`
	UserID     uuid.UUID  `json:"user_id"` // timer.*, member.*
}

// recipient — кому отправить личное уведомление.
type recipient struct {
	ID       uuid.UUID
	ChatID   int64 // личный чат = telegram_user_id
	Settings []byte
}

// recipients — получатели из ids без by: активные, с привязанным Telegram, с включённым
// уведомлением setting; если projectID задан — ещё и участники проекта (иначе уведомление
// раскрыло бы чужой проект). Порядок и дубли ids сохраняются как есть, кроме повторов.
func (p *Personal) recipients(ctx context.Context, ids []uuid.UUID, by uuid.UUID, setting string, projectID *uuid.UUID) ([]recipient, error) {
	var want []uuid.UUID
	for _, id := range ids {
		if id != uuid.Nil && id != by && !containsID(want, id) {
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	q := `SELECT u.id, u.telegram_user_id AS chat_id, u.notification_settings::text AS settings FROM users u
		WHERE u.id IN ? AND u.is_active AND u.telegram_user_id IS NOT NULL`
	args := []any{want}
	if projectID != nil {
		q += ` AND EXISTS (SELECT 1 FROM project_members m WHERE m.user_id = u.id AND m.project_id = ?)`
		args = append(args, *projectID)
	}
	var rows []recipient
	if err := p.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("telegram: recipients: %w", err)
	}
	out := rows[:0]
	for _, r := range rows {
		if user.EffectiveNotifications(events.JSON(r.Settings))[setting] {
			out = append(out, r)
		}
	}
	return out, nil
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// notify ставит в очередь одно сообщение каждому получателю. Если очередь отказала на середине,
// событие повторится и первые получатели могут получить сообщение дважды: доставка «хотя бы раз»,
// как у всех обработчиков событий (повтор возможен только при сбое Redis).
func (p *Personal) notify(ctx context.Context, to []recipient, text string, kb *models.InlineKeyboardMarkup) error {
	for _, r := range to {
		if err := p.out.Send(ctx, Message{Op: OpSend, ChatID: r.ChatID, Text: text, Keyboard: kb}, 0); err != nil {
			return err
		}
	}
	return nil
}

// name — display_name пользователя (кто действовал).
func (p *Personal) name(ctx context.Context, id uuid.UUID) (string, error) {
	var name string
	if err := p.db.WithContext(ctx).Raw(`SELECT display_name FROM users WHERE id = ?`, id).Scan(&name).Error; err != nil {
		return "", fmt.Errorf("telegram: user name: %w", err)
	}
	return name, nil
}

func decodeEvent(env events.Envelope, v any) error {
	if err := json.Unmarshal(env.Payload, v); err != nil {
		return fmt.Errorf("telegram: %s payload: %w", env.Type, err)
	}
	return nil
}

// taskURL — карточка задачи в веб-интерфейсе (кнопка «Открыть», §8.3).
func (p *Personal) taskURL(key string) string { return p.baseURL + "/tasks/" + key }

// keyboard — один ряд кнопок; nil, если кнопок нет.
func keyboard(row ...models.InlineKeyboardButton) *models.InlineKeyboardMarkup {
	if len(row) == 0 {
		return nil
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
}

func (p *Personal) openButton(key string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: Text("btn_open", nil), URL: p.taskURL(key)}
}

// taskText — данные шаблонов уведомлений по задаче.
type taskText struct {
	Key, Title, By, Comment, Body string
}

// taskAssigned — «Вас назначили исполнителем» с кнопками «Взять в работу» (пока задача в todo)
// и «Открыть».
func (p *Personal) taskAssigned(ctx context.Context, env events.Envelope) error {
	var e personalEvent
	if err := decodeEvent(env, &e); err != nil || e.AssigneeID == nil {
		return err
	}
	to, err := p.recipients(ctx, []uuid.UUID{*e.AssigneeID}, e.By, user.NotifyAssigned, &e.ProjectID)
	if err != nil || len(to) == 0 {
		return err
	}
	by, err := p.name(ctx, e.By)
	if err != nil {
		return err
	}
	var status string
	if err := p.db.WithContext(ctx).Raw(`SELECT status FROM tasks WHERE id = ? AND deleted_at IS NULL`, e.TaskID).
		Scan(&status).Error; err != nil {
		return fmt.Errorf("telegram: task status: %w", err)
	}
	if status == "" {
		return nil // задачу уже удалили — назначение неактуально, а «Открыть» вело бы на 404
	}
	var row []models.InlineKeyboardButton
	if status == task.StatusTodo {
		// Та же кнопка, что под карточкой задачи в группе (§8.4, §8.8: take:<task_id>).
		row = append(row, models.InlineKeyboardButton{Text: Text("btn_take", nil), CallbackData: "take:" + e.TaskID.String()})
	}
	row = append(row, p.openButton(e.Key))
	return p.notify(ctx, to, Text("p_assigned", taskText{Key: e.Key, Title: e.Title, By: by}),
		keyboard(row...))
}

// taskTransitioned — доработка (исполнителю), принята или отменена (исполнителю и автору),
// на проверку (автору, с кнопками «Принять» и «Вернуть»). Остальные переходы в личный чат не идут.
func (p *Personal) taskTransitioned(ctx context.Context, env events.Envelope) error {
	var e struct {
		personalEvent
		From    string `json:"from"`
		To      string `json:"to"`
		Comment string `json:"comment"`
	}
	if err := decodeEvent(env, &e); err != nil {
		return err
	}
	var (
		targets []uuid.UUID
		setting string
		tmpl    string
	)
	assignee := []uuid.UUID{}
	if e.AssigneeID != nil {
		assignee = append(assignee, *e.AssigneeID)
	}
	switch {
	case e.From == task.StatusReview && e.To == task.StatusInProgress:
		targets, setting, tmpl = assignee, user.NotifyRework, "p_rework"
	case e.To == task.StatusDone:
		targets, setting, tmpl = append(assignee, e.ReporterID), user.NotifyAcceptedCancelled, "p_accepted"
	case e.To == task.StatusCancelled:
		targets, setting, tmpl = append(assignee, e.ReporterID), user.NotifyAcceptedCancelled, "p_cancelled"
	case e.To == task.StatusReview:
		targets, setting, tmpl = []uuid.UUID{e.ReporterID}, user.NotifyReviewRequested, "p_review"
	default:
		return nil
	}
	to, err := p.recipients(ctx, targets, e.By, setting, &e.ProjectID)
	if err != nil || len(to) == 0 {
		return err
	}
	by, err := p.name(ctx, e.By)
	if err != nil {
		return err
	}
	text := Text(tmpl, taskText{Key: e.Key, Title: e.Title, By: by, Comment: e.Comment})
	if tmpl != "p_review" {
		return p.notify(ctx, to, text, keyboard(p.openButton(e.Key)))
	}
	// §8.7: автор, который и исполнитель, свою задачу не принимает (§4) — кнопки «Принять» нет.
	ownTask := e.AssigneeID != nil && *e.AssigneeID == e.ReporterID
	return p.notify(ctx, to, text, p.reviewKeyboard(e.TaskID, e.Key, !ownTask))
}

// reviewKeyboard — кнопки уведомления «отправлена на проверку».
func (p *Personal) reviewKeyboard(taskID uuid.UUID, key string, accept bool) *models.InlineKeyboardMarkup {
	var row []models.InlineKeyboardButton
	if accept {
		row = append(row, models.InlineKeyboardButton{Text: Text("btn_accept", nil), CallbackData: ActionAccept + ":" + taskID.String()})
	}
	row = append(row, models.InlineKeyboardButton{Text: Text("btn_rework", nil), CallbackData: ActionRework + ":" + taskID.String()},
		p.openButton(key))
	return keyboard(row...)
}

// commentCreated — новый комментарий исполнителю и автору задачи, кроме автора комментария.
// В payload уже первые 200 символов (§8.6).
func (p *Personal) commentCreated(ctx context.Context, env events.Envelope) error {
	var e struct {
		personalEvent
		AuthorID uuid.UUID `json:"author_id"`
		Body     string    `json:"body"`
	}
	if err := decodeEvent(env, &e); err != nil {
		return err
	}
	targets := []uuid.UUID{e.ReporterID}
	if e.AssigneeID != nil {
		targets = append(targets, *e.AssigneeID)
	}
	to, err := p.recipients(ctx, targets, e.AuthorID, user.NotifyComment, &e.ProjectID)
	if err != nil || len(to) == 0 {
		return err
	}
	by, err := p.name(ctx, e.AuthorID)
	if err != nil {
		return err
	}
	return p.notify(ctx, to, Text("p_comment", taskText{Key: e.Key, Title: e.Title, By: by, Body: e.Body}),
		keyboard(p.openButton(e.Key)))
}

// taskDeleted — «Ваша задача удалена» исполнителю и автору, кроме удалившего. Кнопки «Открыть»
// нет: удалённую задачу видит только администратор.
func (p *Personal) taskDeleted(ctx context.Context, env events.Envelope) error {
	var e personalEvent
	if err := decodeEvent(env, &e); err != nil {
		return err
	}
	targets := []uuid.UUID{e.ReporterID}
	if e.AssigneeID != nil {
		targets = append(targets, *e.AssigneeID)
	}
	to, err := p.recipients(ctx, targets, e.By, user.NotifyTaskDeleted, &e.ProjectID)
	if err != nil || len(to) == 0 {
		return err
	}
	by, err := p.name(ctx, e.By)
	if err != nil {
		return err
	}
	return p.notify(ctx, to, Text("p_task_deleted", taskText{Key: e.Key, Title: e.Title, By: by}), nil)
}

// timerStopped — таймер остановлен воркером через 12 часов (§6.4); владельцу таймера.
func (p *Personal) timerStopped(ctx context.Context, env events.Envelope) error {
	var e struct {
		personalEvent
		Minutes int `json:"minutes"`
	}
	if err := decodeEvent(env, &e); err != nil {
		return err
	}
	to, err := p.recipients(ctx, []uuid.UUID{e.UserID}, uuid.Nil, user.NotifyTimerAutoStopped, nil)
	if err != nil || len(to) == 0 {
		return err
	}
	text := Text("p_timer_stopped", map[string]string{"Key": e.Key, "Title": e.Title, "Spent": duration.Format(e.Minutes)})
	return p.notify(ctx, to, text, keyboard(p.openButton(e.Key)))
}

// worklogTrimmed — запись таймера обрезана по дневному лимиту 24 часа (§6.4); владельцу таймера.
func (p *Personal) worklogTrimmed(ctx context.Context, env events.Envelope) error {
	var e struct {
		personalEvent
		WorkDate  string `json:"work_date"`
		Requested int    `json:"requested_minutes"`
		Written   int    `json:"written_minutes"`
	}
	if err := decodeEvent(env, &e); err != nil {
		return err
	}
	to, err := p.recipients(ctx, []uuid.UUID{e.UserID}, uuid.Nil, user.NotifyWorklogTrimmed, nil)
	if err != nil || len(to) == 0 {
		return err
	}
	date := e.WorkDate
	if d, err := time.Parse(time.DateOnly, e.WorkDate); err == nil {
		date = d.Format("02.01.2006") // §7: даты dd.mm.yyyy
	}
	text := Text("p_worklog_trimmed", map[string]string{"Key": e.Key, "Title": e.Title, "Date": date,
		"Spent": duration.Format(e.Written), "Requested": duration.Format(e.Requested)})
	return p.notify(ctx, to, text, keyboard(p.openButton(e.Key)))
}

// membership — добавили в проект, исключили, сменили роль; кроме собственного действия.
// Участие в проекте не проверяется: исключённый уже не участник, но узнать об этом должен.
func (p *Personal) membership(ctx context.Context, env events.Envelope) error {
	var e struct {
		ProjectID uuid.UUID `json:"project_id"`
		Key       string    `json:"key"`
		UserID    uuid.UUID `json:"user_id"`
		Role      string    `json:"role"`
		OldRole   string    `json:"old_role"`
		By        uuid.UUID `json:"by"`
	}
	if err := decodeEvent(env, &e); err != nil {
		return err
	}
	to, err := p.recipients(ctx, []uuid.UUID{e.UserID}, e.By, user.NotifyMembership, nil)
	if err != nil || len(to) == 0 {
		return err
	}
	by, err := p.name(ctx, e.By)
	if err != nil {
		return err
	}
	var name string
	if err := p.db.WithContext(ctx).Raw(`SELECT name FROM projects WHERE id = ?`, e.ProjectID).Scan(&name).Error; err != nil {
		return fmt.Errorf("telegram: project name: %w", err)
	}
	tmpl := map[string]string{events.MemberAdded: "p_member_added", events.MemberRemoved: "p_member_removed",
		events.MemberRoleChanged: "p_member_role"}[env.Type]
	text := Text(tmpl, map[string]string{"Key": e.Key, "Name": name, "By": by,
		"Role": roleName(e.Role), "OldRole": roleName(e.OldRole)})
	return p.notify(ctx, to, text, nil)
}

// roleName — роль участника по-русски (§2).
func roleName(role string) string {
	if role == "" {
		return ""
	}
	return Text("role_"+role, nil)
}
