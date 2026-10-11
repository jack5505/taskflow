package notification

import (
	"context"
	"encoding/json"
	"fmt"

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

// Register подключает к диспетчеру воркера обработчики, создающие уведомления (§7.7).
func Register(d *events.Dispatcher, db *gorm.DB) {
	member := func(typ string) events.Handler {
		return func(ctx context.Context, env events.Envelope) error {
			var p memberPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return fmt.Errorf("notification: %s payload: %w", env.Type, err)
			}
			if p.UserID == p.By {
				return nil // о собственном действии не уведомляем
			}
			eventID, err := uuid.Parse(env.EventID)
			if err != nil {
				return fmt.Errorf("notification: event id: %w", err)
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
	By         uuid.UUID  `json:"by"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
	ReporterID uuid.UUID  `json:"reporter_id"`
}

// taskDeleted — §7.7 «удаление вашей задачи»: исполнителю и автору, кроме удалившего.
func taskDeleted(db *gorm.DB) events.Handler {
	return func(ctx context.Context, env events.Envelope) error {
		var p taskPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("notification: %s payload: %w", env.Type, err)
		}
		eventID, err := uuid.Parse(env.EventID)
		if err != nil {
			return fmt.Errorf("notification: event id: %w", err)
		}
		targets := []uuid.UUID{p.ReporterID}
		if p.AssigneeID != nil && *p.AssigneeID != p.ReporterID {
			targets = append(targets, *p.AssigneeID)
		}
		for _, uid := range targets {
			if uid == p.By {
				continue
			}
			taskID := p.TaskID
			if err := Create(ctx, db, New{UserID: uid, TaskID: &taskID, Type: TypeTaskDeleted, EventID: &eventID,
				Payload: map[string]any{"key": p.Key, "title": p.Title, "by": p.By}}); err != nil {
				return err
			}
		}
		return nil
	}
}
