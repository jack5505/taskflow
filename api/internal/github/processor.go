package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// Processor обрабатывает доставки вебхука в воркере: связывает ветки и PR с задачами по ключу
// (§12.3). Смена статуса по PR (§12.4) — следующий шаг.
type Processor struct {
	db *gorm.DB
}

func NewProcessor(db *gorm.DB) *Processor { return &Processor{db: db} }

// Register подключает обработчик события github.delivery. Подключается всегда, а не только при
// заданном GITHUB_APP_ID: событие могло прийти от API с другими настройками и иначе потерялось бы.
func Register(d *events.Dispatcher, p *Processor) {
	d.On(events.GitHubDelivery, "github", p.handle)
}

// taskKeyRe — ключ задачи в имени ветки или заголовке PR без учёта регистра: «feat/tm-42-login»,
// «TM-42: вход». Слева — не буква и не цифра, чтобы «xtm-42» не считался ключом TM-42.
var taskKeyRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([a-z][a-z0-9]{1,9}-[0-9]+)`)

// keysIn — ключи задач проекта projectKey в текстах, без повторов, в верхнем регистре.
func keysIn(projectKey string, texts ...string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, s := range texts {
		for _, m := range taskKeyRe.FindAllStringSubmatch(s, -1) {
			k := strings.ToUpper(m[1])
			if strings.HasPrefix(k, projectKey+"-") && !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}

type repoRef struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

type pullRequestEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Draft   bool   `json:"draft"`
		Merged  bool   `json:"merged"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
	} `json:"pull_request"`
	Repository repoRef `json:"repository"`
}

type createEvent struct {
	Ref        string  `json:"ref"`
	RefType    string  `json:"ref_type"`
	Repository repoRef `json:"repository"`
	Sender     struct {
		Login string `json:"login"`
	} `json:"sender"`
}

func (p *Processor) handle(ctx context.Context, env events.Envelope) error {
	var d Delivery
	if err := json.Unmarshal(env.Payload, &d); err != nil {
		return fmt.Errorf("github: %s payload: %w", env.Type, err)
	}
	switch d.Event {
	case "pull_request":
		var e pullRequestEvent
		if err := json.Unmarshal(d.Payload, &e); err != nil {
			return fmt.Errorf("github: pull_request: %w", err)
		}
		return p.pullRequest(ctx, &e)
	case "create":
		var e createEvent
		if err := json.Unmarshal(d.Payload, &e); err != nil {
			return fmt.Errorf("github: create: %w", err)
		}
		if e.RefType != "branch" {
			return nil
		}
		login := e.Sender.Login
		return p.link(ctx, e.Repository.FullName, []string{e.Ref}, task.Link{
			Kind: task.LinkBranch, Repo: e.Repository.FullName, Name: e.Ref,
			URL: e.Repository.HTMLURL + "/tree/" + e.Ref, State: task.PROpen, AuthorLogin: nonEmpty(login),
		})
	default:
		slog.DebugContext(ctx, "github: event ignored", "event", d.Event, "delivery", d.Delivery)
		return nil
	}
}

// prState — состояние PR для связи.
func prState(e *pullRequestEvent) string {
	switch {
	case e.PullRequest.Merged:
		return task.PRMerged
	case e.PullRequest.State == "closed":
		return task.PRClosed
	case e.PullRequest.Draft:
		return task.PRDraft
	}
	return task.PROpen
}

func (p *Processor) pullRequest(ctx context.Context, e *pullRequestEvent) error {
	number := e.Number
	return p.link(ctx, e.Repository.FullName, []string{e.PullRequest.Title, e.PullRequest.Head.Ref}, task.Link{
		Kind: task.LinkPR, Repo: e.Repository.FullName, Number: &number, Name: e.PullRequest.Title,
		URL: e.PullRequest.HTMLURL, State: prState(e), AuthorLogin: nonEmpty(e.PullRequest.User.Login),
	})
}

// link связывает ветку или PR l со всеми задачами проекта репозитория, чьи ключи есть в texts.
// Новая связь — событие github.linked, сменившееся состояние PR — github.pr_changed (§12.6).
func (p *Processor) link(ctx context.Context, repo string, texts []string, l task.Link) error {
	db := p.db.WithContext(ctx)
	target, err := project.ByGitHubRepo(db, repo)
	if err != nil || target == nil {
		return err // репозиторий не привязан, проект удалён или в архиве — событие не нужно
	}
	tasks, err := task.LiveByKeys(db, target.ID, keysIn(target.Key, texts...))
	if err != nil || len(tasks) == 0 {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, t := range tasks {
			l.TaskID = t.ID
			before, err := task.LinkState(tx, l)
			if err != nil {
				return err
			}
			if _, err := task.UpsertLink(tx, l); err != nil {
				return err
			}
			payload := map[string]any{"task_id": t.ID, "key": t.Key, "project_id": target.ID, "kind": l.Kind,
				"repo": l.Repo, "number": l.Number, "name": l.Name, "url": l.URL, "state": l.State}
			switch {
			case before == "":
				err = events.Publish(tx, events.GitHubLinked, payload)
			case before != l.State:
				payload["previous_state"] = before
				err = events.Publish(tx, events.GitHubPRChanged, payload)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
