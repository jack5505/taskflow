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
	"github.com/jack5505/taskflow/api/internal/user"
)

// Действия журнала (§3.8): привязка репозитория к проекту.
const (
	AuditRepoBound   = "project.github_bound"
	AuditRepoUnbound = "project.github_unbound"
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

// Bind привязывает репозиторий owner/name к проекту (менеджер, не архив). App должен быть
// установлен в репозиторий; репозиторий другого проекта — 400 (один репозиторий — один проект).
func (s *Service) Bind(ctx context.Context, a *project.Access, fullName string) (*project.Project, error) {
	if !repoName.MatchString(fullName) || strings.HasSuffix(fullName, "/.") || strings.HasSuffix(fullName, "/..") {
		return nil, fieldError("repo", "ожидается owner/name репозитория GitHub")
	}
	repo, err := s.client.Repository(ctx, fullName)
	if errors.Is(err, ErrNotInstalled) {
		return nil, fieldError("repo", "GitHub App не установлен в этот репозиторий или репозитория нет")
	}
	if err != nil {
		return nil, err
	}
	// Имя — как в GitHub: с ним приходят вебхуки (repository.full_name).
	return s.update(ctx, a, &repo.FullName, &repo.InstallationID)
}

// Unbind отвязывает репозиторий проекта; не привязан — ничего не меняется.
func (s *Service) Unbind(ctx context.Context, a *project.Access) (*project.Project, error) {
	return s.update(ctx, a, nil, nil)
}

// update — под блокировкой проекта: права менеджера и архив перепроверяются, репозиторий
// записывается с журналом.
func (s *Service) update(ctx context.Context, a *project.Access, repo *string, inst *int64) (*project.Project, error) {
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
		old := p.GitHubRepo
		if samePtr(old, repo) && sameID(p.GitHubInstallationID, inst) {
			return nil // переустановка App даёт новую установку — её нужно записать
		}
		err = project.SetGitHubRepo(tx, p.ID, repo, inst)
		if errors.Is(err, project.ErrRepoTaken) {
			return fieldError("repo", "репозиторий уже привязан к другому проекту")
		}
		if err != nil {
			return err
		}
		p.GitHubRepo, p.GitHubInstallationID = repo, inst
		action, rec := AuditRepoBound, audit.Record{ProjectID: &p.ID, EntityType: "project", EntityID: p.ID.String(),
			Old: map[string]any{"github_repo": old}, New: map[string]any{"github_repo": repo}}
		if repo == nil {
			action = AuditRepoUnbound
		}
		rec.Action = action
		return audit.Write(tx, rec)
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
