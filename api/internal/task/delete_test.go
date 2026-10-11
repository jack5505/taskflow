package task_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// §4: мягкое удаление менеджером, задача пропадает из списка и карточки; восстанавливает администратор.
func TestDeleteAndRestoreTask(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"Удаляемая","assignee_id":null}`)["key"].(string)
	del := func(token string) int {
		rec, _ := f.do(t, "DELETE", "/api/v1/tasks/"+key, token, "")
		return rec.Code
	}
	restore := func(token string) (int, map[string]any) {
		rec, b := f.do(t, "POST", "/api/v1/tasks/"+key+"/restore", token, "")
		return rec.Code, b
	}

	require.Equal(t, http.StatusForbidden, del(f.member), "исполнитель не удаляет")
	require.Equal(t, http.StatusNotFound, del(f.outsider))
	require.Equal(t, http.StatusNoContent, del(f.manager))
	require.Equal(t, http.StatusNotFound, del(f.manager), "уже удалена")

	rec, _ := f.do(t, "GET", "/api/v1/tasks/"+key, f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	_, p := f.list(t, f.member, "")
	require.NotContains(t, keys(p), key, "нет на доске и в бэклоге")
	_, p = f.list(t, f.member, "q="+key)
	require.Empty(t, p.Items, "нет в поиске")

	code, _ := restore(f.manager)
	require.Equal(t, http.StatusNotFound, code, "менеджер корзину не видит")
	code, _ = restore(f.member)
	require.Equal(t, http.StatusNotFound, code)
	code, b := restore(f.admin)
	require.Equal(t, http.StatusOK, code, "%v", b)
	require.Nil(t, b["deleted_at"])
	require.EqualValues(t, 3, b["version"])
	code, _ = restore(f.admin)
	require.Equal(t, http.StatusOK, code, "не удалённая — без изменений")
	code, _ = restore(f.manager)
	require.Equal(t, http.StatusForbidden, code, "не удалённую менеджер видит, но восстанавливать — не его право")
	_, p = f.list(t, f.member, "")
	require.Contains(t, keys(p), key)

	tk := f.get(t, key)
	var fields []string
	require.NoError(t, f.db.Raw(`SELECT field FROM task_history WHERE task_id = ? AND field = 'deleted_at' ORDER BY created_at`, tk.ID).Scan(&fields).Error)
	require.Len(t, fields, 2, "удаление и восстановление — в истории")
	var actions []string
	require.NoError(t, f.db.Raw(`SELECT action FROM audit_logs WHERE entity_id = ? ORDER BY created_at`, tk.ID.String()).Scan(&actions).Error)
	require.Equal(t, []string{"task.deleted", "task.restored"}, actions, "журнал аудита §3.8")
	for _, typ := range []string{events.TaskDeleted, events.TaskRestored} {
		var n int64
		require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = ? AND payload->>'key' = ?`, typ, key).Scan(&n).Error)
		require.EqualValues(t, 1, n, typ)
	}

	require.NoError(t, f.db.Model(f.p).Update("is_archived", true).Error)
	rec, b2 := f.do(t, "DELETE", "/api/v1/tasks/"+key, f.manager, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, apierr.CodeProjectArchived, errCode(b2))
}

// §7.7: «удаление вашей задачи» — автору и исполнителю, кроме удалившего.
func TestTaskDeletedNotifies(t *testing.T) {
	f := newFixture(t)
	other, _ := f.user(t, false)
	f.join(t, other, project.RoleMember)
	key := f.create(t, f.manager, `{"title":"Чья-то","assignee_id":"`+other.ID.String()+`"}`)["key"].(string)
	// автор — менеджер (он и удаляет), исполнитель — other
	require.NoError(t, task.NewService(f.db, project.NewService(f.db)).Delete(context.Background(), f.managerU, key))

	var env struct {
		ID      string
		Payload []byte
	}
	require.NoError(t, f.db.Raw(`SELECT id::text AS id, payload::text::bytea AS payload FROM outbox_events WHERE type = 'task.deleted' AND payload->>'key' = ?`, key).Row().Scan(&env.ID, &env.Payload))
	d := events.NewDispatcher(nil)
	notification.Register(d, f.db)
	require.NoError(t, d.ProcessTask(context.Background(), envelopeTask(t, env.ID, env.Payload)))

	var n int64
	require.NoError(t, f.db.Model(&notification.Notification{}).Where("user_id = ? AND type = 'task_deleted'", other.ID).Count(&n).Error)
	require.EqualValues(t, 1, n, "исполнителю")
	require.NoError(t, f.db.Model(&notification.Notification{}).Where("user_id = ? AND type = 'task_deleted'", f.managerU.ID).Count(&n).Error)
	require.Zero(t, n, "удалившему — нет")
}

func envelopeTask(t *testing.T, eventID string, payload []byte) *asynq.Task {
	t.Helper()
	b, err := json.Marshal(events.Envelope{EventID: eventID, Type: events.TaskDeleted, Payload: payload})
	require.NoError(t, err)
	return asynq.NewTask(events.TaskTypePrefix+events.TaskDeleted, b)
}
