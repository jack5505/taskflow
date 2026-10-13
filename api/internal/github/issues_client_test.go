package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/github"
)

// REST Issues: исполнитель без доступа (422) — повтор без него; лимит запросов — временная
// ошибка; поиск issue по метке в описании.
func TestIssueClient(t *testing.T) {
	_, keyFile := newFakeGitHub(t)
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/app/installations/7/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok-7", "expires_at": time.Now().Add(time.Hour)})
		case r.Header.Get("Authorization") != "token tok-7":
			http.Error(w, "{}", http.StatusUnauthorized)
		case r.URL.Path == "/repos/acme/limited/issues/1":
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
		case r.Method == "GET" && r.URL.Path == "/repos/acme/x/issues":
			require.Equal(t, "all", r.URL.Query().Get("state"))
			_ = json.NewEncoder(w).Encode([]map[string]any{{"number": 3, "html_url": "u3", "body": "a"},
				{"number": 2, "html_url": "u2", "body": "x <!-- m --> y"}})
		case r.URL.Path == "/repos/acme/x/issues" || r.URL.Path == "/repos/acme/x/issues/5":
			var b map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
			mu.Lock()
			bodies = append(bodies, b)
			mu.Unlock()
			if a, _ := b["assignees"].([]any); len(a) > 0 {
				http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 5, "html_url": "https://github.com/acme/x/issues/5"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := github.NewClient("42", keyFile, srv.URL)
	require.NoError(t, err)
	ctx := context.Background()
	in := github.IssueInput{Title: "TM-1: x", Body: "b", Assignees: []string{"octo"}}

	iss, err := c.CreateIssue(ctx, 7, "acme/x", in)
	require.NoError(t, err)
	require.Equal(t, 5, iss.Number)
	in.State, in.StateReason = "closed", "completed"
	require.NoError(t, c.UpdateIssue(ctx, 7, "acme/x", 5, in))
	require.Len(t, bodies, 4, "каждый запрос с исполнителем повторён без него")
	require.Equal(t, "closed", bodies[3]["state"])
	require.Empty(t, bodies[3]["assignees"])

	err = c.UpdateIssue(ctx, 7, "acme/limited", 1, github.IssueInput{Assignees: []string{}})
	var apiErr *github.APIError
	require.True(t, errors.As(err, &apiErr))
	require.True(t, apiErr.Temporary, "лимит запросов — повторить позже")
	require.True(t, strings.Contains(err.Error(), "rate limit"))

	found, err := c.FindIssue(ctx, 7, "acme/x", "<!-- m -->")
	require.NoError(t, err)
	require.Equal(t, 2, found.Number)
	found, err = c.FindIssue(ctx, 7, "acme/x", "<!-- none -->")
	require.NoError(t, err)
	require.Nil(t, found)
	_, err = c.FindIssue(ctx, 7, "acme/missing", "m")
	require.Error(t, err)
	_, err = c.CreateIssue(ctx, 8, "acme/x", in)
	require.Error(t, err, "установка без токена")
}
