package github

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Действия журнала (§3.8): привязка репозитория к проекту.
const (
	AuditRepoBound   = "project.github_bound"
	AuditRepoUnbound = "project.github_unbound"
	AuditIssues      = "project.github_issues" // включена или выключена публикация в Issues (§12.9)
)

// repoName — owner/name репозитория GitHub.
var repoName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)

var errManagerOnly = apierr.Forbidden("Репозитории GitHub подключает менеджер проекта")

func fieldError(field, reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: field, Reason: reason}})
}

// Service — репозиторий GitHub проекта (§12.1, §12.7).
type Service struct {
	db     *gorm.DB
	client *Client
}

func NewService(db *gorm.DB, client *Client) *Service { return &Service{db: db, client: client} }

// Repositories — репозитории, доступные App: менеджеру хотя бы одного проекта и администратору (§12.6).
func (s *Service) Repositories(ctx context.Context, u *user.User) ([]Repo, error) {
	if !u.IsSystemAdmin {
		ok, err := project.ManagesAny(s.db.WithContext(ctx), u.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errManagerOnly
		}
	}
	return s.client.Repositories(ctx)
}

// Bind привязывает репозиторий owner/name к проекту (менеджер, не архив) и включает или
// выключает публикацию задач в Issues (§12.9). App должен быть установлен в репозиторий;
// репозиторий другого проекта — 400 (один репозиторий — один проект).
func (s *Service) Bind(ctx context.Context, a *project.Access, fullName string, issues bool) (*project.Project, error) {
	if !repoName.MatchString(fullName) || strings.HasSuffix(fullName, "/.") || strings.HasSuffix(fullName, "/..") {
		return nil, fieldError("repo", "ожидается owner/name репозитория GitHub")
	}
	if bound := a.Project.GitHubRepo; bound != nil && strings.EqualFold(*bound, fullName) && a.Project.GitHubIssues != issues {
		// Только переключатель публикации: GitHub не спрашиваем — выключить можно и без App.
		return s.update(ctx, a, bound, a.Project.GitHubInstallationID, issues, "")
	}
	repo, err := s.client.Repository(ctx, fullName)
	if errors.Is(err, ErrNotInstalled) {
		return nil, fieldError("repo", "GitHub App не установлен в этот репозиторий или репозитория нет")
	}
	if err != nil {
		return nil, err
	}
	// Переименованный в GitHub прежний репозиторий отвечает по старому имени уже с новым: связи
	// задач переходят на новое имя, issues продолжают следовать за задачами.
	renamed := ""
	if old := a.Project.GitHubRepo; old != nil && !strings.EqualFold(*old, repo.FullName) {
		if prev, err := s.client.Repository(ctx, *old); err == nil && strings.EqualFold(prev.FullName, repo.FullName) {
			renamed = *old
		}
	}
	// Имя — как в GitHub: с ним приходят вебхуки (repository.full_name).
	return s.update(ctx, a, &repo.FullName, &repo.InstallationID, issues, renamed)
}

// Unbind отвязывает репозиторий проекта и выключает публикацию; не привязан — ничего не меняется.
func (s *Service) Unbind(ctx context.Context, a *project.Access) (*project.Project, error) {
	return s.update(ctx, a, nil, nil, false, "")
}

// update — под блокировкой проекта: права менеджера и архив перепроверяются, репозиторий
// записывается с журналом.
// renamed — прежнее имя того же репозитория (переименован в GitHub): связи задач переходят на repo.
func (s *Service) update(ctx context.Context, a *project.Access, repo *string, inst *int64, issues bool, renamed string) (*project.Project, error) {
	var p *project.Project
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if p, err = project.LockForUpdate(tx, a.Project.ID); err != nil {
			return err
		}
		manager, err := project.ManagerIn(tx, p.ID, a.UserID())
		if err != nil {
			return err
		}
		if !manager {
			return errManagerOnly
		}
		if err := project.EnsureWritable(p); err != nil {
			return err
		}
		old, oldInst, oldIssues := p.GitHubRepo, p.GitHubInstallationID, p.GitHubIssues
		if samePtr(old, repo) && sameID(oldInst, inst) && oldIssues == issues {
			return nil // переустановка App даёт новую установку — её нужно записать
		}
		err = project.SetGitHubRepo(tx, p.ID, repo, inst, issues)
		if errors.Is(err, project.ErrRepoTaken) {
			return fieldError("repo", "репозиторий уже привязан к другому проекту")
		}
		if err != nil {
			return err
		}
		if renamed != "" && old != nil && strings.EqualFold(*old, renamed) {
			if err := task.RenameLinksRepo(tx, p.ID, renamed, *repo); err != nil {
				return err
			}
		}
		p.GitHubRepo, p.GitHubInstallationID, p.GitHubIssues = repo, inst, issues
		if !samePtr(old, repo) || !sameID(oldInst, inst) {
			rec := audit.Record{Action: AuditRepoBound, ProjectID: &p.ID, EntityType: "project", EntityID: p.ID.String(),
				Old: map[string]any{"github_repo": old}, New: map[string]any{"github_repo": repo}}
			if repo == nil {
				rec.Action = AuditRepoUnbound
			}
			if err := audit.Write(tx, rec); err != nil {
				return err
			}
		}
		if oldIssues == issues {
			return nil
		}
		return audit.Write(tx, audit.Record{Action: AuditIssues, ProjectID: &p.ID, EntityType: "project", EntityID: p.ID.String(),
			Old: map[string]any{"github_issues": oldIssues}, New: map[string]any{"github_issues": issues}})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func samePtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
