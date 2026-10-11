package task_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

type trashPage struct {
	Items []struct {
		ID        string
		Key       string
		Title     string
		DeletedAt *time.Time `json:"deleted_at"`
		DeletedBy *struct {
			ID          string
			DisplayName string `json:"display_name"`
		} `json:"deleted_by"`
	}
	NextCursor string `json:"next_cursor"`
}

func (f *fixture) trash(t *testing.T, token, query string) (int, trashPage, map[string]any) {
	t.Helper()
	rec, body := f.do(t, "GET", "/api/v1/projects/"+f.p.Key+"/trash?"+query, token, "")
	var p trashPage
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return rec.Code, p, body
}

func (f *fixture) deleteTask(t *testing.T, token, key string) {
	t.Helper()
	rec, _ := f.do(t, "DELETE", "/api/v1/tasks/"+key, token, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
}

// §2: корзину видит только администратор системы; не участнику — 404, участнику — 403.
func TestTrashRights(t *testing.T) {
	f := newFixture(t)
	for token, want := range map[string]int{f.outsider: http.StatusNotFound, f.member: http.StatusForbidden,
		f.manager: http.StatusForbidden, f.admin: http.StatusOK} {
		code, _, _ := f.trash(t, token, "")
		require.Equal(t, want, code)
	}
	code, _, _ := f.trash(t, f.outsider, "limit=x")
	require.Equal(t, http.StatusNotFound, code, "чужой проект не раскрывается и при кривых параметрах")
	code, _, _ = f.trash(t, f.member, "cursor=@@@")
	require.Equal(t, http.StatusForbidden, code)
	rec, _ := f.do(t, "GET", "/api/v1/projects/NOPE404/trash", f.admin, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// §7.9: только удалённые задачи проекта, новые удаления сверху, кто удалил — по истории.
func TestTrashListsDeletedWithAuthor(t *testing.T) {
	f := newFixture(t)
	adminU, admin := f.user(t, true)
	require.NoError(t, f.db.Model(adminU).Update("display_name", "Админ").Error)
	require.NoError(t, f.db.Model(f.managerU).Update("display_name", "Менеджер").Error)
	k := func(title string) string { return f.create(t, f.member, `{"title":"`+title+`"}`)["key"].(string) }
	alive, first, again, silent := k("Живая"), k("Первая"), k("Повторно"), k("Без истории")

	f.deleteTask(t, f.manager, first)
	f.deleteTask(t, f.manager, again)
	rec, _ := f.do(t, "POST", "/api/v1/tasks/"+again+"/restore", admin, "")
	require.Equal(t, http.StatusOK, rec.Code)
	f.deleteTask(t, admin, again)
	// Удаление без записи в истории (старые данные) — автор неизвестен.
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", silent).Update("deleted_at", time.Now().Add(time.Hour)).Error)

	// Удалённая задача другого проекта в корзину не попадает.
	other := project.New(testutil.UniqueKey(t), "Другой")
	require.NoError(t, f.db.Create(other).Error)
	require.NoError(t, f.db.Exec(`INSERT INTO tasks (project_id, number, key, title, reporter_id, position, deleted_at)
		VALUES (?, 1, ?, 'Чужая', ?, 1, now())`, other.ID, other.Key+"-1", f.memberU.ID).Error)

	code, p, body := f.trash(t, f.admin, "")
	require.Equal(t, http.StatusOK, code, "%v", body)
	require.Empty(t, p.NextCursor)
	got := []string{}
	for _, it := range p.Items {
		got = append(got, it.Key)
		require.NotNil(t, it.DeletedAt, it.Key)
	}
	require.Equal(t, []string{silent, again, first}, got, "новые удаления сверху, живой %s нет", alive)
	require.Nil(t, p.Items[0].DeletedBy, "без истории — null")
	require.Equal(t, adminU.ID.String(), p.Items[1].DeletedBy.ID, "автор — последнего удаления")
	require.Equal(t, "Админ", p.Items[1].DeletedBy.DisplayName)
	require.Equal(t, f.managerU.ID.String(), p.Items[2].DeletedBy.ID)
	require.Equal(t, "Менеджер", p.Items[2].DeletedBy.DisplayName)
	require.Equal(t, "Первая", p.Items[2].Title)

	// Администратору корзина доступна и в удалённом проекте (как журнал).
	require.NoError(t, f.db.Exec(`UPDATE projects SET deleted_at = now() WHERE id = ?`, f.p.ID).Error)
	code, p, _ = f.trash(t, f.admin, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 3)
	code, _, _ = f.trash(t, f.manager, "")
	require.Equal(t, http.StatusNotFound, code, "удалённый проект остальные не видят")
}

// §9.2: курсор (deleted_at, id) — без повторов и пропусков, в том числе при равном deleted_at.
func TestTrashCursor(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 123456000, time.UTC)
	want := map[string]bool{}
	for i := range 5 {
		key := f.create(t, f.member, `{"title":"Задача"}`)["key"].(string)
		when := at
		if i >= 3 {
			when = at.Add(time.Duration(i) * time.Minute)
		}
		require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", key).Update("deleted_at", when).Error)
		want[key] = true
	}

	seen := map[string]bool{}
	var times []time.Time
	cursor, pages := "", 0
	for {
		code, p, body := f.trash(t, f.admin, "limit=2&cursor="+cursor)
		require.Equal(t, http.StatusOK, code, "%v", body)
		require.LessOrEqual(t, len(p.Items), 2)
		for _, it := range p.Items {
			require.False(t, seen[it.Key], "повтор %s", it.Key)
			seen[it.Key] = true
			times = append(times, *it.DeletedAt)
		}
		pages++
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	require.Equal(t, want, seen)
	require.Equal(t, 3, pages)
	for i := 1; i < len(times); i++ {
		require.False(t, times[i].After(times[i-1]), "новые сверху")
	}

	for query, fld := range map[string]string{"limit=-1": "limit", "limit=201": "limit", "limit=x": "limit",
		"cursor=@@@": "cursor", "cursor=YWJj": "cursor", "cursor=eHx5": "cursor"} {
		code, _, body := f.trash(t, f.admin, query)
		require.Equal(t, http.StatusBadRequest, code, query)
		require.Equal(t, fld, field(t, body), query)
	}
}
