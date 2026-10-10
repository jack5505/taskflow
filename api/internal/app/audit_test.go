package app_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

// Критерий #17: каждое действие из §3.8, которое уже есть в API, оставляет запись;
// журнал проекта видит только администратор.
func TestEveryActionLeavesAuditRecord(t *testing.T) {
	w := newWorld(t)
	key := testutil.UniqueKey(t)
	proj := "/api/v1/projects/" + key
	newcomer := w.user(t)

	steps := []struct {
		token, method, path, body string
		want                      int
	}{
		{w.admin, "POST", "/api/v1/projects", `{"key":"` + key + `","name":"Аудит"}`, http.StatusCreated},
		{w.admin, "PUT", proj + "/members/" + w.managerU.ID.String(), `{"role":"manager"}`, http.StatusCreated},
		{w.manager, "PUT", proj + "/members/" + newcomer, `{"role":"member"}`, http.StatusCreated},
		{w.manager, "PUT", proj + "/members/" + newcomer, `{"role":"manager"}`, http.StatusOK},
		{w.manager, "DELETE", proj + "/members/" + newcomer, "", http.StatusNoContent},
		{w.manager, "PATCH", proj, `{"name":"Новое имя","notification_settings":{"daily_summary":true}}`, http.StatusOK},
		{w.admin, "PATCH", proj, `{"is_archived":true}`, http.StatusOK},
		{w.admin, "PATCH", proj, `{"is_archived":false}`, http.StatusOK},
		{w.admin, "DELETE", proj, "", http.StatusNoContent},
		{w.admin, "POST", proj + "/restore", "", http.StatusOK},
	}
	for _, s := range steps {
		rec := w.do(s.method, s.path, s.token, s.body)
		require.Equal(t, s.want, rec.Code, "%s %s: %s", s.method, s.path, rec.Body.String())
	}

	rec := w.do("GET", proj+"/audit?limit=200", w.admin, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var page audit.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	got := map[string]int{}
	for _, e := range page.Items {
		got[e.Action]++
		require.NotNil(t, e.ActorID, e.Action)
		require.Equal(t, audit.SourceWeb, e.Source)
	}
	require.Equal(t, map[string]int{
		audit.ProjectCreated: 1, audit.MemberAdded: 2, audit.MemberRoleChanged: 1, audit.MemberRemoved: 1,
		audit.ProjectUpdated: 1, audit.ProjectArchived: 1, audit.ProjectUnarchived: 1,
		audit.ProjectDeleted: 1, audit.ProjectRestored: 1,
	}, got)
	for _, e := range page.Items {
		if e.Action == audit.ProjectUpdated {
			require.JSONEq(t, `{"name":"Аудит","notification_settings":{"daily_summary":false}}`, string(*e.OldValue))
			require.JSONEq(t, `{"name":"Новое имя","notification_settings":{"daily_summary":true}}`, string(*e.NewValue))
		}
	}

	rec = w.do("GET", proj+"/audit?action=member.added&user_id="+w.managerU.ID.String(), w.admin, "")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 1, "фильтры по действию и пользователю")

	for _, tok := range []string{w.manager, w.member} {
		rec = w.do("GET", proj+"/audit", tok, "")
		require.Equal(t, http.StatusForbidden, rec.Code, "журнал — только администратору")
	}
	for q, field := range map[string]string{"from=вчера": "from", "user_id=x": "user_id", "limit=500": "limit", "cursor=%25%25": "cursor"} {
		rec = w.do("GET", proj+"/audit?"+q, w.admin, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, q)
		require.Contains(t, rec.Body.String(), `"field":"`+field+`"`, q)
	}
}

// Действия с пользователями (вне проекта) тоже пишутся в журнал.
func TestUserActionsLeaveAuditRecords(t *testing.T) {
	w := newWorld(t)
	rec := w.do("POST", "/api/v1/users", w.admin, `{"email":"`+testutil.UniqueEmail(t)+`","display_name":"Новый"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct{ ID string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	path := "/api/v1/users/" + created.ID
	for _, body := range []string{`{"display_name":"Переименован","is_system_admin":true}`, `{"is_active":false}`, `{"is_active":true}`} {
		rec = w.do("PATCH", path, w.admin, body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	var actions []string
	require.NoError(t, w.db.Raw(`SELECT action FROM audit_logs WHERE entity_id = ? AND project_id IS NULL ORDER BY created_at`, created.ID).Scan(&actions).Error)
	require.Equal(t, []string{audit.UserCreated, audit.UserUpdated, audit.UserDeactivated, audit.UserActivated}, actions)
}
