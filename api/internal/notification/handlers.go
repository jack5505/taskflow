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
	d.On(events.MemberAdded, "notification", member(TypeMemberAdded))
	d.On(events.MemberRemoved, "notification", member(TypeMemberRemoved))
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
		targets := []uuid.UUID{p.ReporterID}
		if p.AssigneeID != nil && *p.AssigneeID != p.ReporterID {
			targets = append(targets, *p.AssigneeID)
		}
		projectKey, _, _ := strings.Cut(p.Key, "-")
		for _, uid := range targets {
			if uid == p.By {
				continue
			}
			var member bool
			if err := db.WithContext(ctx).Raw(`SELECT EXISTS (SELECT 1 FROM users u JOIN project_members m ON m.user_id = u.id
				WHERE u.id = ? AND u.is_active AND m.project_id = ?)`, uid, p.ProjectID).Scan(&member).Error; err != nil {
				return fmt.Errorf("notification: member check: %w", err)
			}
			if !member {
				continue
			}
			taskID := p.TaskID
			if err := Create(ctx, db, New{UserID: uid, TaskID: &taskID, Type: TypeTaskDeleted, EventID: &eventID,
				Payload: map[string]any{"key": p.Key, "title": p.Title, "by": p.By, "project_id": p.ProjectID, "project_key": projectKey}}); err != nil {
				return err
			}
		}
		return nil
	}
}
