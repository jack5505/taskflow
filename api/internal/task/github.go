package task

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

// События PR, которые меняют статус задачи (§12.4).
const (
	PROpened      = "opened" // открыт, переоткрыт или выведен из черновика
	PRMergedEvent = "merged"
	PRClosedEvent = "closed" // закрыт без слияния
)

// PRChange — событие PR для задачи: что случилось и кто автор PR (связанный пользователь или nil).
type PRChange struct {
	Key      string
	Event    string
	Repo     string
	Number   int
	AuthorID *uuid.UUID // пользователь со связанным GitHub (§12.5); nil — автор не связан
	Login    string     // логин автора PR — в примечании, если пользователь не связан
}

// prSteps — переходы §4 до цели по событию PR (§12.4); пусто — статус не меняется.
// otherOpen — у задачи есть другие открытые PR: тогда ни «Готово», ни возврат не ставятся.
func prSteps(status, event string, otherOpen bool) []string {
	switch event {
	case PROpened:
		switch status {
		case StatusTodo:
			return []string{StatusInProgress, StatusReview}
		case StatusInProgress:
			return []string{StatusReview}
		}
	case PRMergedEvent:
		if otherOpen {
			return nil
		}
		switch status {
		case StatusInProgress:
			return []string{StatusReview, StatusDone}
		case StatusReview:
			return []string{StatusDone}
		}
	case PRClosedEvent:
		if !otherOpen && status == StatusReview {
			return []string{StatusInProgress}
		}
	}
	return nil
}

// ApplyPR меняет статус задачи по событию PR цепочкой разрешённых переходов §4 (§12.4): от имени
// связанного автора PR или системы, в историю — source github. Задача без исполнителя берётся
// автором PR, если он активный участник проекта; иначе статус не меняется. Удалённая задача,
// удалённый или архивный проект — без изменений. changed — статус сменился.
func (s *Service) ApplyPR(ctx context.Context, c PRChange) (changed bool, err error) {
	ctx = audit.WithSource(ctx, audit.SourceGitHub)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Порядок блокировок как в openTask: пользователь → задача → проект.
		if c.AuthorID != nil {
			if err := user.LockShare(tx, []uuid.UUID{*c.AuthorID}); err != nil {
				return err
			}
		}
		var t Task
		found, err := lockTaskByKey(tx, c.Key, false, forUpdate, &t)
		if err != nil || !found {
			return err
		}
		p, err := project.LockShareLive(tx, t.ProjectID)
		if err != nil || p == nil || p.IsArchived {
			return err
		}
		other, err := OpenPRsExcept(tx, t.ID, c.Repo, c.Number)
		if err != nil {
			return err
		}
		steps := prSteps(t.Status, c.Event, other > 0)
		if len(steps) == 0 {
			return nil
		}
		// Автор шага — связанный активный участник проекта, иначе система с логином в примечании
		// (§12.4, §12.5): чужой проекту пользователь задачу не двигает от своего имени.
		a := actor{user: &user.User{Base: model.Base{ID: uuid.Nil}}, manager: true, admin: true, source: audit.SourceGitHub}
		if c.AuthorID != nil {
			role, err := project.RoleIn(tx, p.ID, *c.AuthorID)
			if err != nil {
				return err
			}
			active, err := user.IsActive(tx, *c.AuthorID)
			if err != nil {
				return err
			}
			if role != "" && active {
				a.user.ID, a.member = *c.AuthorID, true
				tx = tx.WithContext(audit.WithActor(ctx, *c.AuthorID))
			}
		}
		if t.AssigneeID == nil && t.Status == StatusTodo && !a.member {
			return nil // взять задачу некому: автор PR не связан или не участник (§12.4)
		}
		in := TransitionInput{Note: fmt.Sprintf("PR %s#%d", c.Repo, c.Number)}
		if c.Event == PRClosedEvent {
			in.Comment = fmt.Sprintf("PR #%d закрыт без слияния", c.Number)
		}
		if !a.member {
			in.ByName = fmt.Sprintf("GitHub (%s)", c.Login)
			in.Note += ", " + in.ByName
			if in.Comment != "" {
				in.Note += ": " + in.Comment // комментарий без автора невозможен — причина в примечании
			}
		}
		before := t
		for _, to := range steps {
			in.To = to
			if err := applyTransition(tx, &t, a, in); err != nil {
				return err
			}
		}
		if err := stopStaleTaskTimers(tx, &t, s.now()); err != nil {
			return err
		}
		t.Version = before.Version + 1
		if err := tx.Model(&t).Select("*").Omit("CreatedAt").Updates(&t).Error; err != nil {
			return fmt.Errorf("task: github transition: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

// LinkGitHub записывает связь задачи t проекта projectID с веткой или PR l в транзакции tx
// (§12.3): строка связи блокируется, поэтому одновременные доставки не дают двойных событий.
// Доставка старше сохранённой (по updated_at PR) состояние не меняет. Новая связь — событие
// github.linked, сменившееся состояние — github.pr_changed (§12.6). fresh = false — доставка
// запоздала и ничего не изменила: статус задачи по ней не двигается.
func LinkGitHub(tx *gorm.DB, t Titled, projectID uuid.UUID, l Link) (fresh bool, err error) {
	l.TaskID = t.ID
	prev, err := lockLink(tx, l)
	if err != nil {
		return false, err
	}
	if prev == nil {
		inserted, err := insertLink(tx, l)
		if err != nil {
			return false, err
		}
		if inserted {
			return true, events.Publish(tx, events.GitHubLinked, linkPayload(t, projectID, l))
		}
		// Вставили одновременно: дальше — как обновление уже существующей строки.
		if prev, err = lockLink(tx, l); err != nil || prev == nil {
			return false, err
		}
	}
	if prev.SourceUpdatedAt != nil && l.SourceUpdatedAt != nil && l.SourceUpdatedAt.Before(*prev.SourceUpdatedAt) {
		return false, nil // запоздавшая доставка: в базе уже более новое состояние
	}
	if err := updateLink(tx, prev.ID, l); err != nil {
		return false, err
	}
	if prev.State == l.State {
		return true, nil
	}
	p := linkPayload(t, projectID, l)
	p["previous_state"] = prev.State
	return true, events.Publish(tx, events.GitHubPRChanged, p)
}

func linkPayload(t Titled, projectID uuid.UUID, l Link) map[string]any {
	return map[string]any{"task_id": t.ID, "key": t.Key, "project_id": projectID, "kind": l.Kind,
		"repo": l.Repo, "number": l.Number, "name": l.Name, "url": l.URL, "state": l.State}
}
