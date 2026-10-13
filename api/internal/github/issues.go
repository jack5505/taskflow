package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// IssueClient — запросы к Issues GitHub, которые нужны публикации (§12.9).
type IssueClient interface {
	CreateIssue(ctx context.Context, installationID int64, repo string, in IssueInput) (*Issue, error)
	UpdateIssue(ctx context.Context, installationID int64, repo string, number int, in IssueInput) error
	FindIssue(ctx context.Context, installationID int64, repo, marker string) (*Issue, error)
}

// Issues публикует задачи в GitHub Issues и держит issue в актуальном виде (§12.9).
type Issues struct {
	db      *gorm.DB
	client  IssueClient
	baseURL string
}

func NewIssues(db *gorm.DB, client IssueClient, baseURL string) *Issues {
	return &Issues{db: db, client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

// issueEvents — события задачи, после которых issue сверяется с ней.
var issueEvents = []string{events.TaskCreated, events.TaskUpdated, events.TaskTransitioned, events.TaskAssigned,
	events.TaskDeleted, events.TaskRestored}

// RegisterIssues подписывает публикацию на события задач.
func RegisterIssues(d *events.Dispatcher, s *Issues) {
	for _, typ := range issueEvents {
		d.On(typ, "github_issues", s.handle)
	}
}

func (s *Issues) handle(ctx context.Context, env events.Envelope) error {
	var p struct {
		TaskID uuid.UUID `json:"task_id"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return fmt.Errorf("github: issue event: %w", err)
	}
	err := s.Sync(ctx, p.TaskID, env.Type == events.TaskCreated)
	var apiErr *APIError
	if errors.As(err, &apiErr) && !apiErr.Temporary || errors.Is(err, errNotFound) {
		// Нет прав (например, установка не приняла право Issues), репозиторий или issue удалены,
		// GitHub отклонил данные: повтор не поможет. Ошибка — в лог уровня error, чтобы её заметили.
		slog.ErrorContext(ctx, "github: issue sync failed", "task_id", p.TaskID, "event", env.Type, "err", err)
		return nil
	}
	return err
}

// Sync приводит issue задачи taskID к её текущему виду; create — создать issue, если его нет
// (только для новой задачи: старые задачи не публикуются, §12.9). Публикация выключена или
// задача в проекте с другим репозиторием — ничего не делает. Синхронизация одной задачи идёт под
// блокировкой, поэтому повтор события не создаёт второй issue.
func (s *Issues) Sync(ctx context.Context, taskID uuid.UUID, create bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockIssueSync(tx, taskID.String()); err != nil {
			return err
		}
		src, err := task.IssueSourceOf(tx, taskID)
		if err != nil || src == nil {
			return err
		}
		target, err := project.IssuesTarget(tx, src.ProjectID)
		if err != nil || target == nil {
			return err
		}
		link, err := task.IssueOf(tx, taskID)
		if err != nil {
			return err
		}
		if link != nil && !strings.EqualFold(link.Repo, target.Repo) {
			return nil // задачу перенесли в проект с другим репозиторием: прежний issue не трогаем
		}
		in, err := s.input(tx, src)
		if err != nil {
			return err
		}
		var number int
		var url string
		switch {
		case link != nil:
			number, url = *link.Number, link.URL
			if err := s.client.UpdateIssue(ctx, target.InstallationID, target.Repo, number, in); err != nil {
				return err
			}
		case !create || src.Deleted:
			return nil
		default:
			iss, err := s.publish(ctx, target, src, in)
			if err != nil {
				return err
			}
			number, url = iss.Number, iss.HTMLURL
		}
		state := task.PROpen
		if in.State == "closed" {
			state = task.PRClosed
		}
		_, err = task.LinkGitHub(tx, task.Titled{ID: src.ID, Key: src.Key, Title: src.Title}, src.ProjectID, task.Link{
			Kind: task.LinkIssue, Repo: target.Repo, Number: &number, Name: in.Title, URL: url, State: state})
		return err
	})
}

// publish создаёт issue задачи. На повторе события прошлая попытка могла его уже создать (связь
// не успела записаться): тогда сначала ищем его по метке в описании среди последних issues.
func (s *Issues) publish(ctx context.Context, target *project.IssueTarget, src *task.IssueSource, in IssueInput) (*Issue, error) {
	var iss *Issue
	if retried(ctx) {
		var err error
		if iss, err = s.client.FindIssue(ctx, target.InstallationID, target.Repo, marker(src.ID)); err != nil {
			return nil, err
		}
	}
	if iss == nil {
		var err error
		if iss, err = s.client.CreateIssue(ctx, target.InstallationID, target.Repo, in); err != nil {
			return nil, err
		}
		if in.State == "open" {
			return iss, nil
		}
	}
	// Найденный — мог устареть; созданный закрытым не бывает: состояние — отдельной правкой.
	return iss, s.client.UpdateIssue(ctx, target.InstallationID, target.Repo, iss.Number, in)
}

// input — каким должен быть issue задачи src (§12.9).
func (s *Issues) input(tx *gorm.DB, src *task.IssueSource) (IssueInput, error) {
	in := IssueInput{Title: src.Key + ": " + src.Title, State: "open", Assignees: []string{}}
	desc := ""
	if src.Description != nil && strings.TrimSpace(*src.Description) != "" {
		desc = strings.TrimSpace(*src.Description) + "\n\n---\n"
	}
	in.Body = fmt.Sprintf("%s[%s в TaskFlow](%s/tasks/%s)\n\n%s", desc, src.Key, s.baseURL, src.Key, marker(src.ID))
	switch {
	case src.Deleted || src.Status == task.StatusCancelled:
		in.State, in.StateReason = "closed", "not_planned"
	case src.Status == task.StatusDone:
		in.State, in.StateReason = "closed", "completed"
	}
	if src.AssigneeID != nil {
		login, err := user.GitHubLoginOf(tx, *src.AssigneeID)
		if err != nil {
			return in, err
		}
		if login != nil {
			in.Assignees = []string{*login}
		}
	}
	return in, nil
}

// retried — событие обрабатывается повторно; вне asynq (тесты, ручной вызов) — считаем, что да.
func retried(ctx context.Context) bool {
	n, ok := asynq.GetRetryCount(ctx)
	return !ok || n > 0
}

// marker — метка задачи в описании issue: по ней находится issue, созданный без записанной связи.
func marker(taskID uuid.UUID) string { return "<!-- taskflow:task:" + taskID.String() + " -->" }
