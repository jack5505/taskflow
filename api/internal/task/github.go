package task

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
)

// LinkGitHub записывает связь задачи t проекта projectID с веткой или PR l в транзакции tx
// (§12.3): строка связи блокируется, поэтому одновременные доставки не дают двойных событий.
// Доставка старше сохранённой (по updated_at PR) состояние не меняет. Новая связь — событие
// github.linked, сменившееся состояние — github.pr_changed (§12.6).
func LinkGitHub(tx *gorm.DB, t Titled, projectID uuid.UUID, l Link) error {
	l.TaskID = t.ID
	prev, err := lockLink(tx, l)
	if err != nil {
		return err
	}
	if prev == nil {
		inserted, err := insertLink(tx, l)
		if err != nil {
			return err
		}
		if inserted {
			return events.Publish(tx, events.GitHubLinked, linkPayload(t, projectID, l))
		}
		// Вставили одновременно: дальше — как обновление уже существующей строки.
		if prev, err = lockLink(tx, l); err != nil || prev == nil {
			return err
		}
	}
	if prev.SourceUpdatedAt != nil && l.SourceUpdatedAt != nil && l.SourceUpdatedAt.Before(*prev.SourceUpdatedAt) {
		return nil // запоздавшая доставка: в базе уже более новое состояние
	}
	if err := updateLink(tx, prev.ID, l); err != nil {
		return err
	}
	if prev.State == l.State {
		return nil
	}
	p := linkPayload(t, projectID, l)
	p["previous_state"] = prev.State
	return events.Publish(tx, events.GitHubPRChanged, p)
}

func linkPayload(t Titled, projectID uuid.UUID, l Link) map[string]any {
	return map[string]any{"task_id": t.ID, "key": t.Key, "project_id": projectID, "kind": l.Kind,
		"repo": l.Repo, "number": l.Number, "name": l.Name, "url": l.URL, "state": l.State}
}
