package task_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/task"
)

// §12.3: GET /tasks/{key}/github — ветки и PR задачи; повторная запись связи обновляет её; не
// участник — 404.
func TestGitHubLinksEndpoint(t *testing.T) {
	f := newFixture(t)
	created := f.create(t, f.member, `{"title":"С веткой"}`)
	key := created["key"].(string)
	id := uuid.MustParse(created["id"].(string))

	rec, _ := f.do(t, "GET", "/api/v1/tasks/"+key+"/github", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[]`, rec.Body.String())

	seven := 7
	login := "octo"
	tt := task.Titled{ID: id, Key: key}
	pid := uuid.New()
	for _, state := range []string{task.PROpen, task.PRMerged} {
		_, err := task.LinkGitHub(f.db, tt, pid, task.Link{Kind: task.LinkPR, Repo: "acme/x", Number: &seven,
			Name: key + ": вход", URL: "https://github.com/acme/x/pull/7", State: state, AuthorLogin: &login})
		require.NoError(t, err)
	}
	fresh, err := task.LinkGitHub(f.db, tt, pid, task.Link{Kind: task.LinkBranch, Repo: "ACME/x", Name: "feat/x",
		URL: "https://github.com/acme/x/tree/feat/x", State: task.PROpen})
	require.NoError(t, err)
	require.True(t, fresh)

	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/github", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var links []task.Link
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &links))
	require.Len(t, links, 2, "повтор PR обновил связь, а не добавил")
	require.Equal(t, task.LinkBranch, links[0].Kind)
	require.Equal(t, task.PRMerged, links[1].State)

	n, err := task.OpenPRsExcept(f.db, id, "acme/x", 1)
	require.NoError(t, err)
	require.Zero(t, n, "влитый PR не открыт")
	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/github", f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}
