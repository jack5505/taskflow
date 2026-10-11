package project_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

func memberEvents(t *testing.T, f *fixture, eventType string, u *user.User) int64 {
	t.Helper()
	var n int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = ? AND payload->>'user_id' = ?`, eventType, u.ID.String()).Scan(&n).Error)
	return n
}

func TestMembersLifecycle(t *testing.T) {
	f := newFixture(t)
	manager, mToken := f.user(t, false)
	p := f.project(t, map[*user.User]string{manager: project.RoleManager})
	newbie, nToken := f.user(t, false)
	base := "/api/v1/projects/" + p.Key + "/members"

	rec, body := f.do(t, "PUT", base+"/"+newbie.ID.String(), mToken, `{"role":"member"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, newbie.Email, body["email"])
	require.Equal(t, "member", body["role"])
	require.EqualValues(t, 1, memberEvents(t, f, events.MemberAdded, newbie))

	rec, _ = f.do(t, "PUT", base+"/"+newbie.ID.String(), mToken, `{"role":"member"}`)
	require.Equal(t, http.StatusOK, rec.Code, "та же роль — без изменений")
	require.EqualValues(t, 1, memberEvents(t, f, events.MemberAdded, newbie))

	rec, _ = f.do(t, "GET", base, nToken, "")
	require.Equal(t, http.StatusOK, rec.Code, "участник видит состав")
	var list []project.MemberView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2)
	require.Equal(t, manager.ID, list[0].UserID, "менеджеры первыми")

	rec, _ = f.do(t, "PUT", base+"/"+newbie.ID.String(), nToken, `{"role":"manager"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "исполнитель не меняет роли")

	rec, body = f.do(t, "PUT", base+"/"+newbie.ID.String(), mToken, `{"role":"manager"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "manager", body["role"])
	require.EqualValues(t, 1, memberEvents(t, f, events.MemberRoleChanged, newbie))

	rec, _ = f.do(t, "PUT", base+"/"+manager.ID.String(), nToken, `{"role":"member"}`)
	require.Equal(t, http.StatusOK, rec.Code, "второй менеджер может понизить первого")

	rec, body = f.do(t, "DELETE", base+"/"+newbie.ID.String(), nToken, "")
	require.Equal(t, http.StatusConflict, rec.Code, "себя, единственного менеджера, не исключить")
	require.Equal(t, apierr.CodeLastManager, errCode(body))

	rec, body = f.do(t, "PUT", base+"/"+newbie.ID.String(), nToken, `{"role":"member"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "и не понизить")
	require.Equal(t, apierr.CodeLastManager, errCode(body))

	rec, _ = f.do(t, "DELETE", base+"/"+manager.ID.String(), nToken, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.EqualValues(t, 1, memberEvents(t, f, events.MemberRemoved, manager))
	rec, _ = f.do(t, "GET", "/api/v1/projects/"+p.Key, mToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "исключённый больше не видит проект")
	rec, _ = f.do(t, "DELETE", base+"/"+manager.ID.String(), nToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "уже не участник")
}

func TestPutMemberValidation(t *testing.T) {
	f := newFixture(t)
	_, admin := f.user(t, true)
	p := f.project(t, nil)
	inactive, _ := f.user(t, false)
	require.NoError(t, f.db.Model(inactive).Update("is_active", false).Error)
	base := "/api/v1/projects/" + p.Key + "/members/"

	rec, body := f.do(t, "PUT", base+inactive.ID.String(), admin, `{"role":"member"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "user_id", field(t, body))
	rec, _ = f.do(t, "PUT", base+"00000000-0000-4000-8000-000000000000", admin, `{"role":"member"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "PUT", base+"not-a-uuid", admin, `{"role":"member"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	u, _ := f.user(t, false)
	rec, body = f.do(t, "PUT", base+u.ID.String(), admin, `{"role":"owner"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "role", field(t, body))

	rec, _ = f.do(t, "PUT", base+u.ID.String(), admin, `{"role":"manager"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "администратор — не участник, но управляет составом")

	require.NoError(t, f.db.Model(p).Update("is_archived", true).Error)
	rec, body = f.do(t, "DELETE", base+u.ID.String(), admin, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, apierr.CodeProjectArchived, errCode(body))
}

// Неактивный менеджер «единственным» не считается и правилу не мешает.
func TestInactiveManagerDoesNotCount(t *testing.T) {
	f := newFixture(t)
	_, admin := f.user(t, true)
	active, _ := f.user(t, false)
	inactive, _ := f.user(t, false)
	p := f.project(t, map[*user.User]string{active: project.RoleManager, inactive: project.RoleManager})
	require.NoError(t, f.db.Model(inactive).Update("is_active", false).Error)
	base := "/api/v1/projects/" + p.Key + "/members/"

	rec, body := f.do(t, "PUT", base+active.ID.String(), admin, `{"role":"member"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "второй менеджер деактивирован")
	require.Equal(t, apierr.CodeLastManager, errCode(body))
	rec, _ = f.do(t, "DELETE", base+inactive.ID.String(), admin, "")
	require.Equal(t, http.StatusNoContent, rec.Code, "деактивированного менеджера убрать можно")
}

// Два менеджера одновременно понижают друг друга: проходит ровно один.
func TestConcurrentDemotionKeepsOneManager(t *testing.T) {
	f := newFixture(t)
	a, _ := f.user(t, false)
	b, _ := f.user(t, false)
	p := f.project(t, map[*user.User]string{a: project.RoleManager, b: project.RoleManager})

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, pair := range [][2]*user.User{{a, b}, {b, a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acc, err := f.svc.Access(context.Background(), pair[0], p.Key)
			if err != nil {
				errs[i] = err
				return
			}
			_, _, errs[i] = f.svc.PutMember(context.Background(), acc, pair[1].ID, project.RoleMember)
		}()
	}
	wg.Wait()
	failed := 0
	for _, err := range errs {
		if err != nil {
			code := apierr.From(err).Code
			require.Contains(t, []string{apierr.CodeLastManager, apierr.CodeForbidden}, code, err.Error())
			failed++
		}
	}
	require.Equal(t, 1, failed)
	var managers int64
	require.NoError(t, f.db.Model(&project.Member{}).Where("project_id = ? AND role = 'manager'", p.ID).Count(&managers).Error)
	require.EqualValues(t, 1, managers)
}

// Ревью #81: деактивированного участника нельзя повысить до менеджера, понизить — можно.
func TestInactiveMemberCannotBecomeManager(t *testing.T) {
	f := newFixture(t)
	_, admin := f.user(t, true)
	x, _ := f.user(t, false)
	m, _ := f.user(t, false)
	p := f.project(t, map[*user.User]string{x: project.RoleMember, m: project.RoleManager})
	require.NoError(t, f.db.Model(x).Update("is_active", false).Error)
	base := "/api/v1/projects/" + p.Key + "/members/"

	rec, body := f.do(t, "PUT", base+x.ID.String(), admin, `{"role":"manager"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "user_id", field(t, body))
	rec, _ = f.do(t, "PUT", base+x.ID.String(), admin, `{"role":"member"}`)
	require.Equal(t, http.StatusOK, rec.Code, "та же роль — без изменений")
}

func TestArchivedMembersWinOverValidation(t *testing.T) {
	f := newFixture(t)
	_, admin := f.user(t, true)
	p := f.project(t, nil)
	require.NoError(t, f.db.Model(p).Update("is_archived", true).Error)
	u, _ := f.user(t, false)
	rec, body := f.do(t, "PUT", "/api/v1/projects/"+p.Key+"/members/"+u.ID.String(), admin, `{"role":"owner"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, apierr.CodeProjectArchived, errCode(body))
}

// Ревью #81: пока деактивация X не закоммичена, повышение X ждёт его строку и после коммита
// видит, что X неактивен, — менеджером деактивированный пользователь не станет.
func TestPromotionWaitsForDeactivation(t *testing.T) {
	f := newFixture(t)
	a, _ := f.user(t, false)
	x, _ := f.user(t, false)
	p := f.project(t, map[*user.User]string{a: project.RoleManager, x: project.RoleMember})
	acc, err := f.svc.Access(context.Background(), a, p.Key)
	require.NoError(t, err)

	deactivation := f.db.Begin()
	require.NoError(t, deactivation.Exec(`SELECT id FROM users WHERE id = ? FOR UPDATE`, x.ID).Error)
	require.NoError(t, deactivation.Model(&user.User{}).Where("id = ?", x.ID).Update("is_active", false).Error)

	done := make(chan error, 1)
	go func() {
		_, _, err := f.svc.PutMember(context.Background(), acc, x.ID, project.RoleManager)
		done <- err
	}()
	select {
	case err := <-done:
		deactivation.Rollback()
		t.Fatalf("PutMember не дождался строки пользователя: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, deactivation.Commit().Error)

	err = <-done
	require.Error(t, err)
	require.Equal(t, apierr.CodeValidation, apierr.From(err).Code, err.Error())
	var role string
	require.NoError(t, f.db.Raw(`SELECT role FROM project_members WHERE project_id = ? AND user_id = ?`, p.ID, x.ID).Scan(&role).Error)
	require.Equal(t, project.RoleMember, role)
}
