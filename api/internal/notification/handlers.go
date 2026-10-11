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

// base — общие поля уведомления о задаче: ключ, название, кто, проект.
func (p taskPayload) base() map[string]any {
	projectKey, _, _ := strings.Cut(p.Key, "-")
	return map[string]any{"key": p.Key, "title": p.Title, "by": p.By, "project_id": p.ProjectID, "project_key": projectKey}
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
		return notifyTaskPeople(ctx, db, p.ProjectID, p.TaskID, p.ReporterID, p.AssigneeID, p.By, TypeTaskDeleted, p.base(), eventID)
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
		return notifyUsers(ctx, db, p.ProjectID, p.TaskID, []uuid.UUID{p.AssigneeID}, p.By, TypeAssigned, p.base(), eventID)
	}
}

// taskTransitioned — §7.7 «смена статуса» и «возврат на доработку»: review → in_progress — исполнителю
// (rework, с причиной); → review — автору (review_requested), исполнителю — смена статуса; остальные
// переходы — исполнителю и автору (status). Если переход сменил исполнителя, прежний тоже узнаёт о
// смене статуса, а новый — нет: ему пришло назначение (task.assigned). Кроме того, кто перевёл;
// только активным участникам проекта.
func taskTransitioned(db *gorm.DB) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p struct {
			taskPayload
			From         string     `json:"from"`
			To           string     `json:"to"`
			Comment      string     `json:"comment"`
			PrevAssignee *uuid.UUID `json:"prev_assignee_id"`
		}
		eventID, err := decode(env, &p)
		if err != nil {
			return err
		}
		data := p.base()
		data["from"], data["to"] = p.From, p.To
		if p.Comment != "" {
			data["comment"] = p.Comment
		}
		// prev_assignee_id есть (в том числе null), только если переход сменил исполнителя.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(env.Payload, &raw); err != nil {
			return fmt.Errorf("notification: %s payload: %w", env.Type, err)
		}
		_, reassigned := raw["prev_assignee_id"]
		var status []uuid.UUID // кому — смена статуса
		switch {
		case p.From == "review" && p.To == "in_progress":
			if p.AssigneeID == nil {
				return nil
			}
			return notifyUsers(ctx, db, p.ProjectID, p.TaskID, []uuid.UUID{*p.AssigneeID}, p.By, TypeRework, data, eventID)
		case p.To == "review":
			if err := notifyUsers(ctx, db, p.ProjectID, p.TaskID, []uuid.UUID{p.ReporterID}, p.By, TypeReview, data, eventID); err != nil {
				return err
			}
		default:
			status = append(status, p.ReporterID)
		}
		if p.AssigneeID != nil && *p.AssigneeID != p.ReporterID && !reassigned {
			status = append(status, *p.AssigneeID)
		}
		if p.PrevAssignee != nil && *p.PrevAssignee != p.ReporterID {
			status = append(status, *p.PrevAssignee)
		}
		return notifyUsers(ctx, db, p.ProjectID, p.TaskID, status, p.By, TypeStatus, data, eventID)
	}
}

// userDeactivated — §3.1: менеджеры проектов, где освобождены задачи деактивированного, получают
// одно уведомление на менеджера (уникальность — событие и получатель) со списком задач по проектам.
// Проект задачи — из строки задачи, а не из префикса ключа. Деактивировавший себя не уведомляет.
func userDeactivated(db *gorm.DB) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p struct {
			UserID   uuid.UUID  `json:"user_id"`
			Released []string   `json:"released_tasks"`
			By       *uuid.UUID `json:"by"`
		}
		eventID, err := decode(env, &p)
		if err != nil || len(p.Released) == 0 {
			return err
		}
		var name string
		if err := db.WithContext(ctx).Raw(`SELECT display_name FROM users WHERE id = ?`, p.UserID).Scan(&name).Error; err != nil {
			return fmt.Errorf("notification: deactivated name: %w", err)
		}
		var rows []struct {
			ManagerID  uuid.UUID
			ProjectID  uuid.UUID
			ProjectKey string
			TaskKey    string
		}
		if err := db.WithContext(ctx).Raw(`SELECT mm.user_id AS manager_id, pr.id AS project_id, pr.key AS project_key, t.key AS task_key
			FROM tasks t JOIN projects pr ON pr.id = t.project_id AND pr.deleted_at IS NULL
			JOIN project_members mm ON mm.project_id = pr.id AND mm.role = 'manager' AND mm.user_id <> ?
			JOIN users mu ON mu.id = mm.user_id AND mu.is_active
			WHERE t.key IN ? ORDER BY mm.user_id, pr.key, t.number`, p.UserID, p.Released).Scan(&rows).Error; err != nil {
			return fmt.Errorf("notification: deactivated managers: %w", err)
		}
		type project struct {
			ProjectID  uuid.UUID `json:"project_id"`
			ProjectKey string    `json:"project_key"`
			Tasks      []string  `json:"released_tasks"`
		}
		byManager := map[uuid.UUID][]project{}
		var order []uuid.UUID
		for _, r := range rows {
			list, seen := byManager[r.ManagerID]
			if !seen {
				order = append(order, r.ManagerID)
			}
			if n := len(list); n == 0 || list[n-1].ProjectID != r.ProjectID {
				list = append(list, project{ProjectID: r.ProjectID, ProjectKey: r.ProjectKey})
			}
			list[len(list)-1].Tasks = append(list[len(list)-1].Tasks, r.TaskKey)
			byManager[r.ManagerID] = list
		}
		for _, m := range order {
			if p.By != nil && *p.By == m {
				continue // о собственном действии не уведомляем
			}
			if err := Create(ctx, db, New{UserID: m, Type: TypeDeactivated, EventID: &eventID, Payload: map[string]any{
				"user_id": p.UserID, "display_name": name, "by": p.By, "projects": byManager[m],
			}}); err != nil {
				return err
			}
		}
		return nil
	}
}
