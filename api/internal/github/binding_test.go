package github_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/github"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// fakeGitHub — REST API GitHub для App 42: две установки, у второй 101 репозиторий (две страницы).
type fakeGitHub struct {
	*httptest.Server
	key          *rsa.PrivateKey
	tokenCalls   atomic.Int32
	installation atomic.Int64 // установка App в acme/backend
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyFile := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}), 0o600))
	f := &fakeGitHub{key: k}
	f.installation.Store(7)
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f, keyFile
}

// appAuth — запрос подписан JWT App 42 её ключом.
func (f *fakeGitHub) appAuth(r *http.Request) bool {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	tok, err := jwt.ParseWithClaims(raw, &jwt.RegisteredClaims{}, func(*jwt.Token) (any, error) { return &f.key.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}))
	return err == nil && tok.Claims.(*jwt.RegisteredClaims).Issuer == "42"
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	repos := func(inst string, n int) []map[string]any {
		var out []map[string]any
		for i := range n {
			name := fmt.Sprintf("acme/%s-%03d", inst, i)
			out = append(out, map[string]any{"full_name": name, "private": i%2 == 0, "html_url": "https://github.com/" + name})
		}
		return out
	}
	page := r.URL.Query().Get("page")
	path := strings.ToLower(r.URL.Path) // GitHub не различает регистр owner/name
	switch {
	case (path == "/repos/acme/backend/installation" || path == "/repos/acme/old-backend/installation") && f.appAuth(r):
		_ = json.NewEncoder(w).Encode(map[string]any{"id": f.installation.Load()})
	case strings.HasPrefix(path, "/repos/") && f.appAuth(r):
		http.NotFound(w, r)
	case (path == "/repos/acme/backend" || path == "/repos/acme/old-backend") && strings.HasPrefix(r.Header.Get("Authorization"), "token tok-"):
		// acme/old-backend переименован в acme/backend: GitHub отвечает уже с новым именем.
		_ = json.NewEncoder(w).Encode(map[string]any{"full_name": "acme/backend", "private": true, "html_url": "https://github.com/acme/backend"})
	case r.URL.Path == "/app/installations" && f.appAuth(r):
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7}, {"id": 8}, {"id": 9}})
	case r.Method == "POST" && r.URL.Path == "/app/installations/9/access_tokens":
		http.Error(w, `{"message":"This installation has been suspended"}`, http.StatusForbidden)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/app/installations/") && f.appAuth(r):
		f.tokenCalls.Add(1)
		id := strings.Split(r.URL.Path, "/")[3]
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok-" + id, "expires_at": time.Now().Add(time.Hour)})
	case r.URL.Path == "/installation/repositories" && r.Header.Get("Authorization") == "token tok-7":
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []map[string]any{{"full_name": "acme/backend", "private": true, "html_url": "https://github.com/acme/backend"}}})
	case r.URL.Path == "/installation/repositories" && r.Header.Get("Authorization") == "token tok-8":
		all := repos("web", 101)
		if page == "2" {
			all = all[100:]
		} else {
			all = all[:100]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": all})
	default:
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}
}

type bindEnv struct {
	db  *gorm.DB
	svc *github.Service
	gh  *fakeGitHub
}

func newBindEnv(t *testing.T) *bindEnv {
	t.Helper()
	gh, keyFile := newFakeGitHub(t)
	client, err := github.NewClient("42", keyFile, gh.URL)
	require.NoError(t, err)
	db := testutil.SharedDB(t)
	require.NoError(t, db.Exec(`UPDATE projects SET github_repo = NULL, github_installation_id = NULL, github_issues = false`).Error)
	return &bindEnv{db: db, svc: github.NewService(db, client), gh: gh}
}

func (e *bindEnv) user(t *testing.T, admin bool) *user.User {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), "x", "Пользователь")
	u.IsSystemAdmin = admin
	require.NoError(t, e.db.Create(u).Error)
	return u
}

func (e *bindEnv) project(t *testing.T, members map[*user.User]string) *project.Project {
	t.Helper()
	p := project.New(testutil.UniqueKey(t), "Проект")
	require.NoError(t, e.db.Create(p).Error)
	for u, role := range members {
		require.NoError(t, e.db.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: role}).Error)
	}
	return p
}

func (e *bindEnv) access(t *testing.T, u *user.User, p *project.Project) *project.Access {
	t.Helper()
	a, err := project.NewService(e.db).Access(context.Background(), u, p.Key)
	require.NoError(t, err)
	return a
}

func code(err error) string {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// §12.1, §12.7: менеджер привязывает репозиторий с установленным App; один репозиторий — один
// проект; отвязка; журнал.
func TestBindRepository(t *testing.T) {
	e := newBindEnv(t)
	ctx := context.Background()
	m := e.user(t, false)
	p := e.project(t, map[*user.User]string{m: project.RoleManager})
	other := e.project(t, map[*user.User]string{m: project.RoleManager})

	got, err := e.svc.Bind(ctx, e.access(t, m, p), "acme/backend", false)
	require.NoError(t, err)
	require.Equal(t, "acme/backend", *got.GitHubRepo)
	require.EqualValues(t, 7, *got.GitHubInstallationID)

	_, err = e.svc.Bind(ctx, e.access(t, m, other), "ACME/Backend", false)
	require.Equal(t, apierr.CodeValidation, code(err), "репозиторий другого проекта (регистр не важен)")

	// Переустановка App: та же привязка с новой установкой записывается.
	e.gh.installation.Store(70)
	got, err = e.svc.Bind(ctx, e.access(t, m, p), "ACME/backend", false)
	require.NoError(t, err)
	require.Equal(t, "acme/backend", *got.GitHubRepo, "имя — как в GitHub")
	require.EqualValues(t, 70, *got.GitHubInstallationID)

	_, err = e.svc.Bind(ctx, e.access(t, m, other), "acme/missing", false)
	require.Equal(t, apierr.CodeValidation, code(err), "App не установлен")
	for _, bad := range []string{"not a repo", "acme/..", "acme/."} {
		_, err = e.svc.Bind(ctx, e.access(t, m, other), bad, false)
		require.Equal(t, apierr.CodeValidation, code(err), bad)
	}

	// §12.9: публикация в Issues включается вместе с привязкой и выключается отвязкой.
	got, err = e.svc.Bind(ctx, e.access(t, m, p), "acme/backend", true)
	require.NoError(t, err)
	require.True(t, got.GitHubIssues)
	require.True(t, e.access(t, m, p).Project.GitHubIssues)

	_, err = e.svc.Unbind(ctx, e.access(t, m, p))
	require.NoError(t, err)
	require.False(t, e.access(t, m, p).Project.GitHubIssues, "отвязка выключает публикацию")
	var repo *string
	require.NoError(t, e.db.Raw(`SELECT github_repo FROM projects WHERE id = ?`, p.ID).Scan(&repo).Error)
	require.Nil(t, repo)
	_, err = e.svc.Bind(ctx, e.access(t, m, other), "acme/backend", false)
	require.NoError(t, err, "после отвязки репозиторий свободен")

	var actions []string
	require.NoError(t, e.db.Raw(`SELECT action FROM audit_logs WHERE project_id = ? ORDER BY created_at`, p.ID).Scan(&actions).Error)
	require.Equal(t, []string{github.AuditRepoBound, github.AuditRepoBound, github.AuditIssues, github.AuditRepoUnbound, github.AuditIssues}, actions)

	// Удалённый проект репозиторий не держит.
	require.NoError(t, project.NewService(e.db).Delete(ctx, other.Key))
	_, err = e.svc.Bind(ctx, e.access(t, m, p), "acme/backend", false)
	require.NoError(t, err, "после удаления проекта репозиторий свободен")
}

// §12.9: переключатель публикации меняется без обращения к GitHub (App уже снят); переименованный
// в GitHub репозиторий переносит связи задач на новое имя.
func TestBindIssuesToggleAndRename(t *testing.T) {
	e := newBindEnv(t)
	ctx := context.Background()
	m := e.user(t, false)
	p := e.project(t, map[*user.User]string{m: project.RoleManager})
	require.NoError(t, e.db.Exec(`UPDATE projects SET github_repo = 'acme/gone', github_installation_id = 7, github_issues = true WHERE id = ?`, p.ID).Error)
	got, err := e.svc.Bind(ctx, e.access(t, m, p), "ACME/gone", false)
	require.NoError(t, err, "выключение без GitHub")
	require.False(t, got.GitHubIssues)
	_, err = e.svc.Bind(ctx, e.access(t, m, p), "acme/gone", false)
	require.Equal(t, apierr.CodeValidation, code(err), "без смены переключателя — проверка в GitHub")

	tk := uuid.New()
	require.NoError(t, e.db.Exec(`UPDATE projects SET github_repo = 'acme/old-backend' WHERE id = ?`, p.ID).Error)
	reporter := e.user(t, false)
	require.NoError(t, e.db.Exec(`INSERT INTO tasks (id, project_id, number, key, title, type, status, priority, reporter_id, position, version)
		VALUES (?, ?, 1, ?, 'x', 'task', 'todo', 'medium', ?, 1, 1)`, tk, p.ID, p.Key+"-1", reporter.ID).Error)
	require.NoError(t, e.db.Exec(`INSERT INTO github_links (id, task_id, kind, repo, number, name, url, state)
		VALUES (?, ?, 'issue', 'acme/old-backend', 1, 'x', 'u', 'open')`, uuid.New(), tk).Error)
	got, err = e.svc.Bind(ctx, e.access(t, m, p), "acme/backend", true)
	require.NoError(t, err)
	require.Equal(t, "acme/backend", *got.GitHubRepo)
	var repo string
	require.NoError(t, e.db.Raw(`SELECT repo FROM github_links WHERE task_id = ?`, tk).Scan(&repo).Error)
	require.Equal(t, "acme/backend", repo, "связи перешли на новое имя")
}

// §12.6: привязывает менеджер; участник — 403; архив — 409.
func TestBindRights(t *testing.T) {
	e := newBindEnv(t)
	ctx := context.Background()
	m, member := e.user(t, false), e.user(t, false)
	p := e.project(t, map[*user.User]string{m: project.RoleManager, member: project.RoleMember})
	_, err := e.svc.Bind(ctx, e.access(t, member, p), "acme/backend", false)
	require.Equal(t, apierr.CodeForbidden, code(err))
	require.NoError(t, e.db.Model(&project.Project{}).Where("id = ?", p.ID).Update("is_archived", true).Error)
	_, err = e.svc.Bind(ctx, e.access(t, m, p), "acme/backend", false)
	require.Equal(t, apierr.CodeProjectArchived, code(err))
}

// Список репозиториев: все установки, все страницы; приостановленная установка пропускается;
// список и токены кэшируются; не менеджер — 403.
func TestRepositories(t *testing.T) {
	e := newBindEnv(t)
	ctx := context.Background()
	m, nobody, admin := e.user(t, false), e.user(t, false), e.user(t, true)
	e.project(t, map[*user.User]string{m: project.RoleManager})

	repos, err := e.svc.Repositories(ctx, m)
	require.NoError(t, err)
	require.Len(t, repos, 1+101)
	require.Equal(t, "acme/backend", repos[0].FullName)
	require.True(t, repos[0].Private)
	_, err = e.svc.Repositories(ctx, admin)
	require.NoError(t, err)
	require.EqualValues(t, 2, e.gh.tokenCalls.Load(), "токены установок взяты один раз и кэшированы")

	_, err = e.svc.Repositories(ctx, nobody)
	require.Equal(t, apierr.CodeForbidden, code(err))
}

// Ключ App не читается — ошибка клиента.
func TestNewClientBadKey(t *testing.T) {
	_, err := github.NewClient("42", filepath.Join(t.TempDir(), "missing.pem"), "http://x")
	require.Error(t, err)
}
