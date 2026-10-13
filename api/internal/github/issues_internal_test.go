package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// fakeIssues — Issues одного репозитория в памяти.
type fakeIssues struct {
	mu      sync.Mutex
	issues  map[int]*IssueInput
	creates int
	fail    error // ошибка следующего запроса
	lost    bool  // CreateIssue создаёт issue, но отвечает ошибкой (обрыв связи)
}

func newFakeIssues() *fakeIssues { return &fakeIssues{issues: map[int]*IssueInput{}} }

func (f *fakeIssues) take() error {
	err := f.fail
	f.fail = nil
	return err
}

func (f *fakeIssues) CreateIssue(_ context.Context, inst int64, _ string, in IssueInput) (*Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.take(); err != nil {
		return nil, err
	}
	if inst != 7 {
		return nil, fmt.Errorf("installation %d", inst)
	}
	f.creates++
	n := len(f.issues) + 1
	in.State = "open" // GitHub создаёт открытым
	in.StateReason = ""
	f.issues[n] = &in
	if f.lost {
		f.lost = false
		return nil, &APIError{Status: http.StatusBadGateway, Temporary: true, msg: "502"}
	}
	return &Issue{Number: n, HTMLURL: fmt.Sprintf("https://github.com/acme/x/issues/%d", n)}, nil
}

func (f *fakeIssues) UpdateIssue(_ context.Context, _ int64, _ string, number int, in IssueInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.take(); err != nil {
		return err
	}
	if f.issues[number] == nil {
		return errNotFound
	}
	f.issues[number] = &in
	return nil
}

func (f *fakeIssues) FindIssue(_ context.Context, _ int64, _, marker string) (*Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for n, in := range f.issues {
		if strings.Contains(in.Body, marker) {
			return &Issue{Number: n, HTMLURL: fmt.Sprintf("https://github.com/acme/x/issues/%d", n), Body: in.Body}, nil
		}
	}
	return nil, nil
}

func (f *fakeIssues) get(n int) IssueInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.issues[n]
}

type issueEnv struct {
	*procEnv
	gh *fakeIssues
	s  *Issues
}

func newIssueEnv(t *testing.T) *issueEnv {
	t.Helper()
	e := newProcEnv(t)
	require.NoError(t, e.db.Model(&project.Project{}).Where("id = ?", e.prj.ID).Update("github_issues", true).Error)
	gh := newFakeIssues()
	return &issueEnv{procEnv: e, gh: gh, s: NewIssues(e.db, gh, "https://tasks.example/")}
}

func (e *issueEnv) event(t *testing.T, typ string, tk *task.Task) error {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"task_id": tk.ID})
	require.NoError(t, err)
	return e.s.handle(context.Background(), events.Envelope{Type: typ, Payload: raw})
}

func (e *issueEnv) issue(t *testing.T, tk *task.Task) *task.Link {
	t.Helper()
	l, err := task.IssueOf(e.db, tk.ID)
	require.NoError(t, err)
	return l
}

// §12.9: новая задача получает issue; повтор события не создаёт второй; issue следует за
// задачей — заголовок, описание, исполнитель, закрытие и переоткрытие.
func TestIssuesFollowTask(t *testing.T) {
	e := newIssueEnv(t)
	tk := e.t1
	require.NoError(t, e.event(t, events.TaskUpdated, tk))
	require.Zero(t, e.gh.creates, "старая задача (без task.created) не публикуется")

	require.NoError(t, e.event(t, events.TaskCreated, tk))
	require.NoError(t, e.event(t, events.TaskCreated, tk))
	require.Equal(t, 1, e.gh.creates, "повтор события — тот же issue")
	l := e.issue(t, tk)
	require.NotNil(t, l)
	require.Equal(t, task.LinkIssue, l.Kind)
	require.Equal(t, 1, *l.Number)
	require.Equal(t, task.PROpen, l.State)
	in := e.gh.get(1)
	require.Equal(t, tk.Key+": Задача", in.Title)
	require.Contains(t, in.Body, "https://tasks.example/tasks/"+tk.Key)
	require.Contains(t, in.Body, marker(tk.ID))
	require.Empty(t, in.Assignees)

	assignee := user.New(testutil.UniqueEmail(t), "x", "Исполнитель")
	require.NoError(t, e.db.Create(assignee).Error)
	login := "gh-" + e.prj.Key
	require.NoError(t, user.SetGitHubLogin(e.db, assignee.ID, &login))
	require.NoError(t, e.db.Model(&task.Task{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"title": "Новое название", "description": "Подробности", "assignee_id": assignee.ID, "status": task.StatusDone}).Error)
	require.NoError(t, e.event(t, events.TaskTransitioned, tk))
	in = e.gh.get(1)
	require.Equal(t, tk.Key+": Новое название", in.Title)
	require.True(t, strings.HasPrefix(in.Body, "Подробности\n\n---\n"))
	require.Equal(t, []string{login}, in.Assignees)
	require.Equal(t, "closed", in.State)
	require.Equal(t, "completed", in.StateReason)
	require.Equal(t, task.PRClosed, e.issue(t, tk).State)
	require.EqualValues(t, 1, e.events(t, events.GitHubPRChanged, tk), "состояние issue сменилось")

	require.NoError(t, e.db.Model(&task.Task{}).Where("id = ?", tk.ID).Update("status", task.StatusInProgress).Error)
	require.NoError(t, e.event(t, events.TaskTransitioned, tk))
	require.Equal(t, "open", e.gh.get(1).State, "переоткрытие")

	require.NoError(t, e.db.Exec(`UPDATE tasks SET deleted_at = now() WHERE id = ?`, tk.ID).Error)
	require.NoError(t, e.event(t, events.TaskDeleted, tk))
	in = e.gh.get(1)
	require.Equal(t, "closed", in.State)
	require.Equal(t, "not_planned", in.StateReason, "удалённая задача — не будет сделана")
	require.Equal(t, 1, e.gh.creates)

	// Удалённая до публикации задача issue не получает.
	require.NoError(t, e.db.Exec(`UPDATE tasks SET deleted_at = now() WHERE id = ?`, e.t2.ID).Error)
	require.NoError(t, e.event(t, events.TaskCreated, e.t2))
	require.Nil(t, e.issue(t, e.t2))
}

// Публикация выключена — ничего; задача в проекте с другим репозиторием — прежний issue не
// трогаем; отменённая — закрыт как не планируемый.
func TestIssuesDisabledAndMoved(t *testing.T) {
	e := newIssueEnv(t)
	require.NoError(t, e.db.Model(&project.Project{}).Where("id = ?", e.prj.ID).Update("github_issues", false).Error)
	require.NoError(t, e.event(t, events.TaskCreated, e.t1))
	require.Zero(t, e.gh.creates)

	require.NoError(t, e.db.Model(&project.Project{}).Where("id = ?", e.prj.ID).Update("github_issues", true).Error)
	require.NoError(t, e.db.Model(&task.Task{}).Where("id = ?", e.t2.ID).Update("status", task.StatusCancelled).Error)
	require.NoError(t, e.event(t, events.TaskCreated, e.t2))
	in := e.gh.get(1)
	require.Equal(t, "closed", in.State, "созданный закрывается отдельной правкой")
	require.Equal(t, "not_planned", in.StateReason)

	require.NoError(t, e.db.Exec(`UPDATE github_links SET repo = 'acme/old' WHERE task_id = ?`, e.t2.ID).Error)
	require.NoError(t, e.db.Model(&task.Task{}).Where("id = ?", e.t2.ID).Update("title", "Другое").Error)
	require.NoError(t, e.event(t, events.TaskUpdated, e.t2))
	require.NotContains(t, e.gh.get(1).Title, "Другое")
}

// Обрыв после создания issue: повтор находит его по метке, а не создаёт второй. Постоянная
// ошибка GitHub — без повтора; временная — повтор.
func TestIssuesRecoveryAndErrors(t *testing.T) {
	e := newIssueEnv(t)
	e.gh.lost = true
	require.Error(t, e.event(t, events.TaskCreated, e.t1), "временная ошибка — событие повторится")
	require.Nil(t, e.issue(t, e.t1))
	require.NoError(t, e.event(t, events.TaskCreated, e.t1))
	require.Equal(t, 1, e.gh.creates, "issue найден по метке")
	require.NotNil(t, e.issue(t, e.t1))

	e.gh.fail = &APIError{Status: http.StatusForbidden, msg: "403"}
	require.NoError(t, e.event(t, events.TaskUpdated, e.t1), "нет прав — повтор не поможет")
	e.gh.fail = &APIError{Status: http.StatusForbidden, Temporary: true, msg: "rate limit"}
	require.Error(t, e.event(t, events.TaskUpdated, e.t1))
	require.NoError(t, e.db.Exec(`UPDATE github_links SET number = 99 WHERE task_id = ?`, e.t1.ID).Error)
	require.NoError(t, e.event(t, events.TaskUpdated, e.t1), "issue удалён в GitHub — пропуск")
	require.Error(t, e.s.handle(context.Background(), events.Envelope{Type: events.TaskUpdated, Payload: []byte("{")}))
	require.NoError(t, e.s.Sync(context.Background(), uuid.New(), true), "задачи нет")
	RegisterIssues(events.NewDispatcher(nil), e.s)
}

// Одновременные task.created одной задачи — один issue.
func TestIssuesConcurrentCreate(t *testing.T) {
	e := newIssueEnv(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.s.Sync(context.Background(), e.t1.ID, true)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, e.gh.creates)
}
