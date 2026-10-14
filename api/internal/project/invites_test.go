package project_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

const inviteBaseURL = "https://tasks.test"

// createInvite — ссылка через API, возвращает токен из url.
func createInvite(t *testing.T, f *fixture, key, token, role string) (id, raw string) {
	t.Helper()
	rec, body := f.do(t, "POST", "/api/v1/projects/"+key+"/invites", token, `{"role":"`+role+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	url := body["url"].(string)
	raw, ok := strings.CutPrefix(url, inviteBaseURL+"/join/")
	require.True(t, ok, url)
	return body["id"].(string), raw
}

// clientIP — свой адрес на тест: лимит регистраций считается по IP в общем Redis пакета.
func clientIP() string {
	return fmt.Sprintf("203.0.%d.%d", rand.IntN(256), 1+rand.IntN(250))
}

// register — POST /invites/{token}/register без входа с адреса ip.
func register(t *testing.T, f *fixture, raw, ip, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/invites/"+raw+"/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = ip + ":40000"
	f.r.ServeHTTP(rec, r)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

func registerBody(email string) string {
	return `{"email":"` + email + `","display_name":"  Новый Участник ","password":"` + password + `"}`
}

func TestInviteRegisterJoinsProject(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	_, raw := createInvite(t, f, p.Key, mToken, project.RoleMember)

	rec, body := f.do(t, "GET", "/api/v1/invites/"+raw, "", "")
	require.Equal(t, http.StatusOK, rec.Code, "страница приглашения открывается без входа")
	require.Equal(t, p.Key, body["project_key"])
	require.Equal(t, "Проект", body["project_name"])
	require.Equal(t, "member", body["role"])

	email := testutil.UniqueEmail(t)
	rec, body = register(t, f, raw, clientIP(), registerBody(email))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, body["access_token"], "регистрация сразу входит")
	require.Contains(t, rec.Header().Get("Set-Cookie"), "refresh_token=")
	newToken := body["access_token"].(string)
	require.Equal(t, "Новый Участник", body["user"].(map[string]any)["display_name"])

	rec, body = f.do(t, "GET", "/api/v1/projects/"+p.Key, newToken, "")
	require.Equal(t, http.StatusOK, rec.Code, "новый пользователь видит проект")
	require.Equal(t, "member", body["my_role"])

	u, err := user.ByEmail(f.db, email)
	require.NoError(t, err)
	require.False(t, u.IsSystemAdmin)
	require.EqualValues(t, 1, memberEvents(t, f, events.MemberAdded, u))
	var actions []string
	require.NoError(t, f.db.Raw(`SELECT action FROM audit_logs WHERE actor_id = ? ORDER BY created_at, action`, u.ID).Scan(&actions).Error)
	require.ElementsMatch(t, []string{audit.UserCreated, audit.MemberAdded}, actions, "актор аудита — сам новый пользователь")

	rec, _ = f.do(t, "GET", "/api/v1/projects/"+p.Key+"/invites", mToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var list []project.InviteView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)
	require.Equal(t, 1, list[0].JoinedCount)
	require.Equal(t, manager.ID, *list[0].CreatedBy)
	require.Equal(t, "Пользователь", list[0].CreatedByName)

	rec, body = register(t, f, raw, clientIP(), registerBody(strings.ToUpper(email)))
	require.Equal(t, http.StatusBadRequest, rec.Code, "email занят без учёта регистра")
	require.Equal(t, "email", field(t, body))
}

func TestInviteAcceptKeepsRole(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	other, oToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	_, raw := createInvite(t, f, p.Key, mToken, project.RoleManager)

	rec, _ := f.do(t, "POST", "/api/v1/invites/"+raw+"/accept", "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "accept — только после входа")

	rec, body := f.do(t, "POST", "/api/v1/invites/"+raw+"/accept", oToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, true, body["added"])
	require.Equal(t, "manager", body["role"], "роль из ссылки")
	require.Equal(t, p.Key, body["project_key"])
	require.EqualValues(t, 1, memberEvents(t, f, events.MemberAdded, other))

	rec, body = f.do(t, "POST", "/api/v1/invites/"+raw+"/accept", oToken, "")
	require.Equal(t, http.StatusOK, rec.Code, "повторно — без изменений")
	require.Equal(t, false, body["added"])
	require.EqualValues(t, 1, memberEvents(t, f, events.MemberAdded, other))

	_, rawMember := createInvite(t, f, p.Key, mToken, project.RoleMember)
	rec, body = f.do(t, "POST", "/api/v1/invites/"+rawMember+"/accept", mToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, false, body["added"])
	require.Equal(t, "manager", body["role"], "менеджер по ссылке исполнителя не понижается")
}

func TestInviteRights(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	member, nToken := f.user(t, false)
	_, adminToken := f.user(t, true)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager, member: project.RoleMember})
	base := "/api/v1/projects/" + p.Key + "/invites"

	rec, _ := f.do(t, "POST", base, nToken, `{"role":"member"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "исполнитель ссылок не создаёт")
	rec, _ = f.do(t, "GET", base, nToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, body := f.do(t, "POST", base, mToken, `{"role":"admin"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "role", field(t, body))

	id, _ := createInvite(t, f, p.Key, adminToken, project.RoleMember)
	rec, _ = f.do(t, "DELETE", base+"/"+id, nToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, _ = f.do(t, "DELETE", base+"/"+id, mToken, "")
	require.Equal(t, http.StatusNoContent, rec.Code, "менеджер отзывает ссылку администратора")
	rec, _ = f.do(t, "DELETE", base+"/"+id, mToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "уже отозвана")
	rec, _ = f.do(t, "DELETE", base+"/not-a-uuid", mToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	other := f.project(t, map[*user.User]string{manager: project.RoleManager})
	id, _ = createInvite(t, f, other.Key, mToken, project.RoleMember)
	rec, _ = f.do(t, "DELETE", base+"/"+id, mToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "ссылку другого проекта через этот не отозвать")
}

func TestInviteInvalidLinks(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	_, oToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})

	revokedID, revoked := createInvite(t, f, p.Key, mToken, project.RoleMember)
	rec, _ := f.do(t, "DELETE", "/api/v1/projects/"+p.Key+"/invites/"+revokedID, mToken, "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	_, expired := createInvite(t, f, p.Key, mToken, project.RoleMember)
	require.NoError(t, f.db.Exec(`UPDATE project_invites SET expires_at = now() - interval '1 second' WHERE token_hash = ?`,
		auth.HashLinkToken(expired)).Error)
	rec, _ = f.do(t, "GET", "/api/v1/projects/"+p.Key+"/invites", mToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "[]", strings.TrimSpace(rec.Body.String()), "отозванные и истёкшие в списке не показываются")

	archived := f.project(t, map[*user.User]string{manager: project.RoleManager})
	_, inArchive := createInvite(t, f, archived.Key, mToken, project.RoleMember)
	require.NoError(t, f.db.Model(&project.Project{}).Where("id = ?", archived.ID).Update("is_archived", true).Error)

	deleted := f.project(t, map[*user.User]string{manager: project.RoleManager})
	_, inDeleted := createInvite(t, f, deleted.Key, mToken, project.RoleMember)
	require.NoError(t, f.db.Delete(deleted).Error)

	for name, raw := range map[string]string{"отозвана": revoked, "истекла": expired, "архив": inArchive, "удалён": inDeleted, "нет такой": "nope"} {
		rec, body := f.do(t, "GET", "/api/v1/invites/"+raw, "", "")
		require.Equal(t, http.StatusNotFound, rec.Code, name)
		require.Equal(t, apierr.CodeNotFound, errCode(body), name)
		rec, _ = f.do(t, "POST", "/api/v1/invites/"+raw+"/accept", oToken, "")
		require.Equal(t, http.StatusNotFound, rec.Code, name)
		rec, _ = register(t, f, raw, clientIP(), registerBody(testutil.UniqueEmail(t)))
		require.Equal(t, http.StatusNotFound, rec.Code, name)
	}

	rec, _ = f.do(t, "POST", "/api/v1/projects/"+archived.Key+"/invites", mToken, `{"role":"member"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "в архивном проекте ссылки не создаются")
}

func TestInviteRegisterValidation(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	_, raw := createInvite(t, f, p.Key, mToken, project.RoleMember)
	ip := clientIP()

	rec, body := register(t, f, raw, ip, `{"email":"`+testutil.UniqueEmail(t)+`","display_name":"   ","password":"`+password+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "display_name", field(t, body))
	rec, body = register(t, f, raw, ip, `{"email":"`+testutil.UniqueEmail(t)+`","display_name":"Имя","password":"short"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "password", field(t, body))
	rec, body = register(t, f, raw, ip, `{"email":"not-an-email","display_name":"Имя","password":"`+password+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "email", field(t, body))
	rec, body = register(t, f, raw, ip, registerBody(manager.Email))
	require.Equal(t, http.StatusBadRequest, rec.Code, "занятый email")
	require.Equal(t, "email", field(t, body))
}

func TestInviteRegisterRateLimitedByIP(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	_, raw := createInvite(t, f, p.Key, mToken, project.RoleMember)
	ip := clientIP()

	for i := range 10 {
		rec, _ := register(t, f, raw, ip, registerBody(testutil.UniqueEmail(t)))
		require.Equal(t, http.StatusOK, rec.Code, "попытка %d", i+1)
	}
	rec, body := register(t, f, raw, ip, registerBody(testutil.UniqueEmail(t)))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, apierr.CodeRateLimited, errCode(body))
	rec, _ = register(t, f, raw, clientIP(), registerBody(testutil.UniqueEmail(t)))
	require.Equal(t, http.StatusOK, rec.Code, "другой IP не заблокирован")

	// За доверенным прокси IP берётся из X-Forwarded-For: подделанный левый край не помогает.
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/invites/"+raw+"/register", strings.NewReader(registerBody(testutil.UniqueEmail(t))))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "198.51.100.7, "+ip)
	r.RemoteAddr = "172.18.0.5:40000"
	f.r.ServeHTTP(rr, r)
	require.Equal(t, http.StatusTooManyRequests, rr.Code, rr.Body.String())
}

func TestRevokeRacesWithJoin(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	for range 5 {
		id, raw := createInvite(t, f, p.Key, mToken, project.RoleMember)
		_, token := f.user(t, false)
		var wg sync.WaitGroup
		var joined, revoked int
		wg.Go(func() {
			rec, _ := f.do(t, "POST", "/api/v1/invites/"+raw+"/accept", token, "")
			joined = rec.Code
		})
		wg.Go(func() {
			rec, _ := f.do(t, "DELETE", "/api/v1/projects/"+p.Key+"/invites/"+id, mToken, "")
			revoked = rec.Code
		})
		wg.Wait()
		require.Equal(t, http.StatusNoContent, revoked)
		var count int
		require.NoError(t, f.db.Raw(`SELECT joined_count FROM project_invites WHERE id = ?`, id).Scan(&count).Error)
		if joined == http.StatusOK {
			require.Equal(t, 1, count)
		} else {
			require.Equal(t, http.StatusNotFound, joined)
			require.Equal(t, 0, count)
		}
	}
}

func TestRemovedMemberCannotRejoinByOldLink(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	other, oToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	_, raw := createInvite(t, f, p.Key, mToken, project.RoleMember)

	rec, _ := f.do(t, "POST", "/api/v1/invites/"+raw+"/accept", oToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = f.do(t, "DELETE", "/api/v1/projects/"+p.Key+"/members/"+other.ID.String(), mToken, "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec, body := f.do(t, "POST", "/api/v1/invites/"+raw+"/accept", oToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code, "исключённый по старой ссылке не возвращается")
	require.Equal(t, apierr.CodeForbidden, errCode(body))

	_, fresh := createInvite(t, f, p.Key, mToken, project.RoleMember)
	rec, _ = f.do(t, "POST", "/api/v1/invites/"+fresh+"/accept", oToken, "")
	require.Equal(t, http.StatusOK, rec.Code, "по новой ссылке — можно")
}

func TestArchiveAndDeleteRevokeInvites(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	_, adminToken := f.user(t, true)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	_, raw := createInvite(t, f, p.Key, mToken, project.RoleMember)

	rec, _ := f.do(t, "PATCH", "/api/v1/projects/"+p.Key, adminToken, `{"is_archived":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec, _ = f.do(t, "PATCH", "/api/v1/projects/"+p.Key, adminToken, `{"is_archived":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec, _ = f.do(t, "GET", "/api/v1/invites/"+raw, "", "")
	require.Equal(t, http.StatusNotFound, rec.Code, "после возврата из архива ссылка не оживает")

	_, raw = createInvite(t, f, p.Key, mToken, project.RoleMember)
	rec, _ = f.do(t, "DELETE", "/api/v1/projects/"+p.Key, adminToken, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	rec, _ = f.do(t, "POST", "/api/v1/projects/"+p.Key+"/restore", adminToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec, _ = f.do(t, "GET", "/api/v1/invites/"+raw, "", "")
	require.Equal(t, http.StatusNotFound, rec.Code, "после восстановления ссылка не оживает")
}
