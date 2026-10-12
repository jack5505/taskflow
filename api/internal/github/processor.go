package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Processor обрабатывает доставки вебхука в воркере: связывает ветки и PR с задачами по ключу
// (§12.3) и меняет статус задач по PR (§12.4).
type Processor struct {
	db    *gorm.DB
	tasks *task.Service
}

func NewProcessor(db *gorm.DB, tasks *task.Service) *Processor {
	return &Processor{db: db, tasks: tasks}
}

// Register подключает обработчик события github.delivery. Подключается всегда, а не только при
// заданном GITHUB_APP_ID: событие могло прийти от API с другими настройками и иначе потерялось бы.
func Register(d *events.Dispatcher, p *Processor) {
	d.On(events.GitHubDelivery, "github", p.handle)
}

// taskKeyRe — ключ задачи в имени ветки или заголовке PR без учёта регистра: «feat/tm-42-login»,
// «TM-42: вход». Слева — не буква и не цифра, чтобы «xtm-42» не считался ключом TM-42.
var taskKeyRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([a-z][a-z0-9]{1,9})-([0-9]+)`)

// keysIn — ключи задач проекта projectKey в текстах, без повторов, в верхнем регистре; номер
// без ведущих нулей (TM-042 — это TM-42).
func keysIn(projectKey string, texts ...string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, s := range texts {
		for _, m := range taskKeyRe.FindAllStringSubmatch(s, -1) {
			num := strings.TrimLeft(m[2], "0")
			if num == "" || len(num) > 9 { // номер задачи — int, 0 и слишком длинные — не ключи
				continue
			}
			k := strings.ToUpper(m[1]) + "-" + num
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
		UpdatedAt *time.Time `json:"updated_at"`
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
	case "create", "delete":
		var e createEvent
		if err := json.Unmarshal(d.Payload, &e); err != nil {
			return fmt.Errorf("github: %s: %w", d.Event, err)
		}
		if e.RefType != "branch" {
			return nil
		}
		state := task.PROpen
		if d.Event == "delete" {
			state = task.PRClosed // ветка удалена — в карточке она закрыта
		}
		_, err := p.link(ctx, e.Repository.FullName, []string{e.Ref}, task.Link{
			Kind: task.LinkBranch, Repo: e.Repository.FullName, Name: e.Ref,
			URL: e.Repository.HTMLURL + "/tree/" + escapeRef(e.Ref), State: state, AuthorLogin: nonEmpty(e.Sender.Login),
		})
		return err
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
	keys, err := p.link(ctx, e.Repository.FullName, []string{e.PullRequest.Title, e.PullRequest.Head.Ref}, task.Link{
		Kind: task.LinkPR, Repo: e.Repository.FullName, Number: &number, Name: e.PullRequest.Title,
		URL: e.PullRequest.HTMLURL, State: prState(e), AuthorLogin: nonEmpty(e.PullRequest.User.Login),
		SourceUpdatedAt: e.PullRequest.UpdatedAt,
	})
	if err != nil || len(keys) == 0 {
		return err
	}
	event := prEvent(e)
	if event == "" {
		return nil
	}
	author, err := user.ActiveIDByGitHubLogin(p.db.WithContext(ctx), e.PullRequest.User.Login)
	if err != nil {
		return err
	}
	// Каждая задача — своя транзакция: повтор события после сбоя на второй задаче не меняет первую
	// (её статус уже другой, prSteps для него ничего не делает).
	for _, key := range keys {
		if _, err := p.tasks.ApplyPR(ctx, task.PRChange{Key: key, Event: event, Repo: e.Repository.FullName,
			Number: number, AuthorID: author, Login: e.PullRequest.User.Login}); err != nil {
			return err
		}
	}
	return nil
}

// prEvent — событие PR, которое двигает статус (§12.4); черновик и правки статус не меняют.
func prEvent(e *pullRequestEvent) string {
	switch e.Action {
	case "opened", "reopened", "ready_for_review":
		if !e.PullRequest.Draft && e.PullRequest.State == "open" {
			return task.PROpened
		}
	case "closed":
		if e.PullRequest.Merged {
			return task.PRMergedEvent
		}
		return task.PRClosedEvent
	}
	return ""
}

// link связывает ветку или PR l со всеми задачами проекта репозитория, чьи ключи есть в texts,
// и возвращает ключи задач, у которых связь обновилась (запоздавшая доставка статус не двигает). Новая связь — событие github.linked, сменившееся состояние PR —
// github.pr_changed (§12.6).
func (p *Processor) link(ctx context.Context, repo string, texts []string, l task.Link) ([]string, error) {
	db := p.db.WithContext(ctx)
	target, err := project.ByGitHubRepo(db, repo)
	if err != nil || target == nil {
		return nil, err // репозиторий не привязан, проект удалён или в архиве — событие не нужно
	}
	tasks, err := task.LiveByKeys(db, target.ID, keysIn(target.Key, texts...))
	if err != nil || len(tasks) == 0 {
		return nil, err
	}
	var keys []string
	err = db.Transaction(func(tx *gorm.DB) error {
		keys = keys[:0]
		for _, t := range tasks {
			fresh, err := task.LinkGitHub(tx, t, target.ID, l)
			if err != nil {
				return err
			}
			if fresh {
				keys = append(keys, t.Key)
			}
		}
		return nil
	})
	return keys, err
}

// escapeRef — имя ветки для адреса: каждый сегмент экранируется, «/» остаются.
func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
