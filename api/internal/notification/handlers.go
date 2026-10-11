package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
)

type memberPayload struct {
	ProjectID uuid.UUID `json:"project_id"`
	Key       string    `json:"key"`
	UserID    uuid.UUID `json:"user_id"`
	Role      string    `json:"role"`
	OldRole   string    `json:"old_role"`
	By        uuid.UUID `json:"by"`
}

// decode — payload события и его id (для идемпотентности уведомлений).
func decode(env events.Envelope, p any) (uuid.UUID, error) {
	if err := json.Unmarshal(env.Payload, p); err != nil {
		return uuid.Nil, fmt.Errorf("notification: %s payload: %w", env.Type, err)
	}
	id, err := uuid.Parse(env.EventID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("notification: event id: %w", err)
	}
	return id, nil
}

// Register подключает к диспетчеру воркера обработчики, создающие уведомления (§7.7).
func Register(d *events.Dispatcher, db *gorm.DB) {
	member := func(typ string) events.Handler {
		return func(ctx context.Context, env events.Envelope) error {
			var p memberPayload
			eventID, err := decode(env, &p)
			if err != nil {
				return err
			}
			if p.UserID == p.By {
				return nil // о собственном действии не уведомляем
			}
			data := map[string]any{"project_id": p.ProjectID, "project_key": p.Key, "role": p.Role, "by": p.By}
			if p.OldRole != "" {
				data["old_role"] = p.OldRole
			}
			return Create(ctx, db, New{UserID: p.UserID, Type: typ, Payload: data, EventID: &eventID})
		}
	}
	d.On(events.TaskDeleted, "notification", taskDeleted(db))
	d.On(events.CommentCreated, "notification", commentCreated(db))
	d.On(events.MemberAdded, "notification", member(TypeMemberAdded))
	d.On(events.MemberRemoved, "notification", member(TypeMemberRemoved))
	d.On(events.TaskAssigned, "notification", taskAssigned(db))
	d.On(events.TaskTransitioned, "notification", taskTransitioned(db))
	d.On(events.UserDeactivated, "notification", userDeactivated(db))
	d.On(events.TimerAutoStopped, "notification", timerEvent(db, TypeTimerStopped))
	d.On(events.TimerTrimmed, "notification", timerEvent(db, TypeTimerTrimmed))
	// Смена роли в колокольчик не идёт: §7.7 называет только добавление и исключение
	// (о смене роли — личное сообщение бота §8.7).
}

type taskPayload struct {
	TaskID     uuid.UUID  `json:"task_id"`
	Key        string     `json:"key"`
	Title      string     `json:"title"`
	ProjectID  uuid.UUID  `json:"project_id"`
	By         uuid.UUID  `json:"by"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
	ReporterID uuid.UUID  `json:"reporter_id"`
}

// taskDeleted — §7.7 «удаление вашей задачи»: исполнителю и автору, кроме удалившего, и только
// если они ещё активные участники проекта (иначе уведомление раскрыло бы чужой проект).
func taskDeleted(db *gorm.DB) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p taskPayload
		eventID, err := decode(env, &p)
		if err != nil {
			return err
		}
		projectKey, _, _ := strings.Cut(p.Key, "-")
		return notifyTaskPeople(ctx, db, p.ProjectID, p.TaskID, p.ReporterID, p.AssigneeID, p.By, TypeTaskDeleted,
			map[string]any{"key": p.Key, "title": p.Title, "by": p.By, "project_id": p.ProjectID, "project_key": projectKey}, eventID)
	}
}

type commentPayload struct {
	CommentID  uuid.UUID  `json:"comment_id"`
	TaskID     uuid.UUID  `json:"task_id"`
	Key        string     `json:"key"`
	Title      string     `json:"title"`
	ProjectID  uuid.UUID  `json:"project_id"`
	AuthorID   uuid.UUID  `json:"author_id"`
	Body       string     `json:"body"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
	ReporterID uuid.UUID  `json:"reporter_id"`
}

// commentCreated — §7.7 «новый комментарий»: исполнителю и автору задачи, кроме автора комментария,
// если они активные участники проекта.
func commentCreated(db *gorm.DB) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p commentPayload
		eventID, err := decode(env, &p)
		if err != nil {
			return err
		}
		projectKey, _, _ := strings.Cut(p.Key, "-")
		data := map[string]any{"key": p.Key, "title": p.Title, "by": p.AuthorID, "body": p.Body,
			"comment_id": p.CommentID, "project_id": p.ProjectID, "project_key": projectKey}
		return notifyTaskPeople(ctx, db, p.ProjectID, p.TaskID, p.ReporterID, p.AssigneeID, p.AuthorID, TypeComment, data, eventID)
	}
}

// notifyTaskPeople — уведомление автору и исполнителю задачи, кроме действовавшего (by), только
// активным участникам проекта.
func notifyTaskPeople(ctx context.Context, db *gorm.DB, projectID, taskID, reporter uuid.UUID, assignee *uuid.UUID,
	by uuid.UUID, typ string, data map[string]any, eventID uuid.UUID) error {
	targets := []uuid.UUID{reporter}
	if assignee != nil && *assignee != reporter {
		targets = append(targets, *assignee)
	}
	return notifyUsers(ctx, db, projectID, taskID, targets, by, typ, data, eventID)
}

// notifyUsers — уведомление по задаче каждому из targets, кроме by, если он активный участник
// проекта (иначе уведомление раскрыло бы чужой проект).
func notifyUsers(ctx context.Context, db *gorm.DB, projectID, taskID uuid.UUID, targets []uuid.UUID,
	by uuid.UUID, typ string, data map[string]any, eventID uuid.UUID) error {
	for _, uid := range targets {
		if uid == by {
			continue
		}
		var member bool
		if err := db.WithContext(ctx).Raw(`SELECT EXISTS (SELECT 1 FROM users u JOIN project_members m ON m.user_id = u.id
			WHERE u.id = ? AND u.is_active AND m.project_id = ?)`, uid, projectID).Scan(&member).Error; err != nil {
			return fmt.Errorf("notification: member check: %w", err)
		}
		if !member {
			continue
		}
		id := taskID
		if err := Create(ctx, db, New{UserID: uid, TaskID: &id, Type: typ, EventID: &eventID, Payload: data}); err != nil {
			return err
		}
	}
	return nil
}

// timerEvent — §7.7 «автоостановка таймера и обрезание записи по дневному лимиту»: владельцу
// таймера, весь payload события — в уведомление (ключ, минуты, дата).
func timerEvent(db *gorm.DB, typ string) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var data map[string]any
		eventID, err := decode(env, &data)
		if err != nil {
			return err
		}
		userID, err1 := uuid.Parse(fmt.Sprint(data["user_id"]))
		taskID, err2 := uuid.Parse(fmt.Sprint(data["task_id"]))
		if err1 != nil || err2 != nil {
			return fmt.Errorf("notification: %s: нет user_id или task_id", env.Type)
		}
		return Create(ctx, db, New{UserID: userID, TaskID: &taskID, Type: typ, Payload: data, EventID: &eventID})
	}
}

// taskAssigned — §7.7 «назначение»: назначенному, если назначил не он сам и он активный участник.
func taskAssigned(db *gorm.DB) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p struct {
			taskPayload
			AssigneeID uuid.UUID `json:"assignee_id"`
		}
		eventID, err := decode(env, &p)
		if err != nil {
			return err
		}
		projectKey, _, _ := strings.Cut(p.Key, "-")
		data := map[string]any{"key": p.Key, "title": p.Title, "by": p.By, "project_id": p.ProjectID, "project_key": projectKey}
		return notifyUsers(ctx, db, p.ProjectID, p.TaskID, []uuid.UUID{p.AssigneeID}, p.By, TypeAssigned, data, eventID)
	}
}

// taskTransitioned — §7.7 «смена статуса» и «возврат на доработку»: review → in_progress — исполнителю
// (rework, с причиной); → review — автору (review_requested); остальные переходы — исполнителю и
// автору (status). Кроме того, кто перевёл; только активным участникам проекта.
func taskTransitioned(db *gorm.DB) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p struct {
			taskPayload
			From    string `json:"from"`
			To      string `json:"to"`
			Comment string `json:"comment"`
		}
		eventID, err := decode(env, &p)
		if err != nil {
			return err
		}
		projectKey, _, _ := strings.Cut(p.Key, "-")
		data := map[string]any{"key": p.Key, "title": p.Title, "by": p.By, "from": p.From, "to": p.To,
			"project_id": p.ProjectID, "project_key": projectKey}
		if p.Comment != "" {
			data["comment"] = p.Comment
		}
		switch {
		case p.From == "review" && p.To == "in_progress":
			if p.AssigneeID == nil {
				return nil
			}
			return notifyUsers(ctx, db, p.ProjectID, p.TaskID, []uuid.UUID{*p.AssigneeID}, p.By, TypeRework, data, eventID)
		case p.To == "review":
			return notifyUsers(ctx, db, p.ProjectID, p.TaskID, []uuid.UUID{p.ReporterID}, p.By, TypeReview, data, eventID)
		}
		return notifyTaskPeople(ctx, db, p.ProjectID, p.TaskID, p.ReporterID, p.AssigneeID, p.By, TypeStatus, data, eventID)
	}
}

// userDeactivated — §3.1: менеджеры проектов деактивированного получают уведомление со списком
// освобождённых задач этого проекта. Проекты — где он участник (строки участия остаются).
func userDeactivated(db *gorm.DB) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p struct {
			UserID   uuid.UUID `json:"user_id"`
			Released []string  `json:"released_tasks"`
		}
		eventID, err := decode(env, &p)
		if err != nil {
			return err
		}
		var rows []struct {
			ProjectID uuid.UUID
			Key       string
			ManagerID uuid.UUID
			Name      string
		}
		if err := db.WithContext(ctx).Raw(`SELECT pr.id AS project_id, pr.key, mm.user_id AS manager_id, du.display_name AS name
			FROM project_members dm JOIN projects pr ON pr.id = dm.project_id AND pr.deleted_at IS NULL
			JOIN users du ON du.id = dm.user_id
			JOIN project_members mm ON mm.project_id = pr.id AND mm.role = 'manager' AND mm.user_id <> dm.user_id
			JOIN users mu ON mu.id = mm.user_id AND mu.is_active
			WHERE dm.user_id = ? ORDER BY pr.key, mm.user_id`, p.UserID).Scan(&rows).Error; err != nil {
			return fmt.Errorf("notification: deactivated managers: %w", err)
		}
		for _, r := range rows {
			released := []string{}
			for _, k := range p.Released {
				if strings.HasPrefix(k, r.Key+"-") {
					released = append(released, k)
				}
			}
			if err := Create(ctx, db, New{UserID: r.ManagerID, Type: TypeDeactivated, EventID: &eventID, Payload: map[string]any{
				"user_id": p.UserID, "display_name": r.Name, "project_id": r.ProjectID, "project_key": r.Key, "released_tasks": released,
			}}); err != nil {
				return err
			}
		}
		return nil
	}
}
