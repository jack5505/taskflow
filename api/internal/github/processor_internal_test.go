package github

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

// §12.3: ключ ищется без учёта регистра, только ключи проекта, без повторов.
func TestKeysIn(t *testing.T) {
	require.Equal(t, []string{"TM-42", "TM-7"}, keysIn("TM", "feat/tm-42-login", "TM-42: вход, TM-7 и AB-1", "xtm-9"))
	require.Empty(t, keysIn("TM", "main", "no keys here"))
	require.Equal(t, []string{"TM-1"}, keysIn("TM", "TM-1"))
}

type procEnv struct {
	db  *gorm.DB
	p   *Processor
	prj *project.Project
	t1  *task.Task
	t2  *task.Task
}

func newProcEnv(t *testing.T) *procEnv {
	t.Helper()
	db := testutil.SharedDB(t)
	u := user.New(testutil.UniqueEmail(t), "x", "Автор")
	require.NoError(t, db.Create(u).Error)
	p := project.New(testutil.UniqueKey(t), "Проект")
	repo := "acme/" + p.Key
	inst := int64(7)
	p.GitHubRepo, p.GitHubInstallationID = &repo, &inst
	require.NoError(t, db.Create(p).Error)
	mk := func(n int) *task.Task {
		tk := &task.Task{ProjectID: p.ID, Number: n, Key: fmt.Sprintf("%s-%d", p.Key, n), Title: "Задача", Type: "task",
			Status: task.StatusTodo, Priority: "medium", ReporterID: u.ID, Position: float64(n), Version: 1}
		tk.ID = uuid.New()
		require.NoError(t, db.Create(tk).Error)
		return tk
	}
	return &procEnv{db: db, p: NewProcessor(db), prj: p, t1: mk(1), t2: mk(2)}
}

func (e *procEnv) deliver(t *testing.T, event string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	d, err := json.Marshal(Delivery{Event: event, Delivery: "d", Payload: raw})
	require.NoError(t, err)
	require.NoError(t, e.p.handle(context.Background(), events.Envelope{Type: events.GitHubDelivery, Payload: d}))
}

func (e *procEnv) pr(action string, number int, title, head, state string, merged, draft bool) map[string]any {
	return map[string]any{"action": action, "number": number,
		"pull_request": map[string]any{"title": title, "html_url": fmt.Sprintf("https://github.com/%s/pull/%d", *e.prj.GitHubRepo, number),
			"state": state, "merged": merged, "draft": draft, "user": map[string]any{"login": "octo"}, "head": map[string]any{"ref": head}},
		"repository": map[string]any{"full_name": *e.prj.GitHubRepo}}
}

func (e *procEnv) links(t *testing.T, tk *task.Task) []task.Link {
	t.Helper()
	l, err := task.LinksOf(e.db, tk.ID)
	require.NoError(t, err)
	return l
}

func (e *procEnv) events(t *testing.T, typ string, tk *task.Task) int64 {
	t.Helper()
	var n int64
	require.NoError(t, e.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = ? AND payload->>'task_id' = ?`, typ, tk.ID.String()).Scan(&n).Error)
	return n
}

// §12.3: ветка и PR связываются с задачами по ключу в имени ветки и заголовке PR; повтор не
// дублирует связь; смена состояния PR — событие; чужой репозиторий и задачи без ключа — нет.
func TestProcessorLinks(t *testing.T) {
	e := newProcEnv(t)
	k1, k2 := e.t1.Key, e.t2.Key
	e.deliver(t, "create", map[string]any{"ref": "feat/" + k1 + "-login", "ref_type": "branch",
		"repository": map[string]any{"full_name": *e.prj.GitHubRepo, "html_url": "https://github.com/" + *e.prj.GitHubRepo},
		"sender":     map[string]any{"login": "octo"}})
	e.deliver(t, "create", map[string]any{"ref": "v1.0", "ref_type": "tag", "repository": map[string]any{"full_name": *e.prj.GitHubRepo}})

	e.deliver(t, "pull_request", e.pr("opened", 7, k2+": вход", "feat/"+k1+"-login", "open", false, true))
	e.deliver(t, "pull_request", e.pr("ready_for_review", 7, k2+": вход", "feat/"+k1+"-login", "open", false, false))
	e.deliver(t, "pull_request", e.pr("closed", 7, k2+": вход", "feat/"+k1+"-login", "closed", true, false))
	e.deliver(t, "pull_request", e.pr("opened", 8, "без ключа", "main", "open", false, false))

	l1 := e.links(t, e.t1)
	require.Len(t, l1, 2, "ветка и PR (ключ в ветке PR)")
	require.Equal(t, task.LinkBranch, l1[0].Kind)
	require.Equal(t, "feat/"+k1+"-login", l1[0].Name)
	require.Equal(t, task.LinkPR, l1[1].Kind)
	require.Equal(t, 7, *l1[1].Number)
	require.Equal(t, task.PRMerged, l1[1].State)
	require.Equal(t, "octo", *l1[1].AuthorLogin)
	require.Len(t, e.links(t, e.t2), 1, "ключ в заголовке PR")

	require.EqualValues(t, 2, e.events(t, events.GitHubLinked, e.t1))
	require.EqualValues(t, 2, e.events(t, events.GitHubPRChanged, e.t1), "draft → open → merged")

	other := e.pr("opened", 9, k1+": чужой", "x", "open", false, false)
	other["repository"] = map[string]any{"full_name": "acme/other"}
	e.deliver(t, "pull_request", other)
	require.Len(t, e.links(t, e.t1), 2, "репозиторий другого проекта не связывается")

	require.NoError(t, e.db.Model(&project.Project{}).Where("id = ?", e.prj.ID).Update("is_archived", true).Error)
	e.deliver(t, "pull_request", e.pr("opened", 10, k1+": архив", "x", "open", false, false))
	require.Len(t, e.links(t, e.t1), 2, "архивный проект связей не получает")
}

// Битый payload — ошибка (событие повторится); неизвестное событие — без ошибки.
func TestProcessorBadPayload(t *testing.T) {
	p := NewProcessor(testutil.SharedDB(t))
	require.Error(t, p.handle(context.Background(), events.Envelope{Type: events.GitHubDelivery, Payload: []byte("{")}))
	d, _ := json.Marshal(Delivery{Event: "pull_request", Payload: json.RawMessage(`"x"`)})
	require.Error(t, p.handle(context.Background(), events.Envelope{Payload: d}))
	d, _ = json.Marshal(Delivery{Event: "ping", Payload: json.RawMessage(`{}`)})
	require.NoError(t, p.handle(context.Background(), events.Envelope{Payload: d}))
	Register(events.NewDispatcher(nil), p)
}
