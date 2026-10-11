package task_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/notification"
)

// process прогоняет через обработчики уведомлений события typ, у которых payload->>field = value.
func (f *fixture) process(t *testing.T, typ, field, value string) {
	t.Helper()
	var rows []struct {
		ID      string
		Payload string
	}
	require.NoError(t, f.db.Raw(`SELECT id::text AS id, payload::text AS payload FROM outbox_events
		WHERE type = ? AND payload->>? = ? ORDER BY created_at`, typ, field, value).Scan(&rows).Error)
	require.NotEmpty(t, rows, "нет событий %s", typ)
	d := events.NewDispatcher(nil)
	notification.Register(d, f.db)
	for _, r := range rows {
		b, err := json.Marshal(events.Envelope{EventID: r.ID, Type: typ, Payload: []byte(r.Payload)})
		require.NoError(t, err)
		require.NoError(t, d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+typ, b)))
	}
}

// notes — payload уведомлений типа typ у пользователя, по времени.
func (f *fixture) notes(t *testing.T, userID uuid.UUID, typ string) []map[string]any {
	t.Helper()
	var rows []string
	require.NoError(t, f.db.Raw(`SELECT payload::text FROM notifications WHERE user_id = ? AND type = ? ORDER BY created_at`,
		userID, typ).Scan(&rows).Error)
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		require.NoError(t, json.Unmarshal([]byte(r), &out[i]))
	}
	return out
}

func (f *fixture) transition(t *testing.T, key, token, body string) {
	t.Helper()
	rec, _ := f.do(t, "POST", "/api/v1/tasks/"+key+"/transition", token, body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// §7.7: назначение (и при создании), отправка на проверку, возврат на доработку с причиной,
// смена статуса; о собственном действии — нет.
func TestTaskEventNotifications(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.manager, `{"title":"Отчёт","assignee_id":"`+f.memberU.ID.String()+`"}`)["key"].(string)
	f.create(t, f.manager, `{"title":"Себе","assignee_id":"`+f.managerU.ID.String()+`"}`)

	f.process(t, events.TaskAssigned, "project_id", f.p.ID.String())
	assigned := f.notes(t, f.memberU.ID, notification.TypeAssigned)
	require.Len(t, assigned, 1, "назначение при создании")
	require.Equal(t, key, assigned[0]["key"])
	require.Equal(t, "Отчёт", assigned[0]["title"])
	require.Empty(t, f.notes(t, f.managerU.ID, notification.TypeAssigned), "себя не уведомляем")

	f.transition(t, key, f.member, `{"to":"in_progress"}`)
	f.transition(t, key, f.member, `{"to":"review"}`)
	f.transition(t, key, f.manager, `{"to":"in_progress","comment":"Нет итогов по месяцу"}`)
	f.transition(t, key, f.member, `{"to":"review"}`)
	f.transition(t, key, f.manager, `{"to":"done"}`)
	f.process(t, events.TaskTransitioned, "key", key)

	status := f.notes(t, f.managerU.ID, notification.TypeStatus)
	require.Len(t, status, 1, "автору: взятие в работу; остальное — отдельными типами")
	require.Equal(t, "in_progress", status[0]["to"])
	review := f.notes(t, f.managerU.ID, notification.TypeReview)
	require.Len(t, review, 2, "автору: задача на проверке")
	rework := f.notes(t, f.memberU.ID, notification.TypeRework)
	require.Len(t, rework, 1)
	require.Equal(t, "Нет итогов по месяцу", rework[0]["comment"])
	done := f.notes(t, f.memberU.ID, notification.TypeStatus)
	require.Len(t, done, 1, "исполнителю: задачу приняли")
	require.Equal(t, "done", done[0]["to"])
	require.Empty(t, f.notes(t, f.memberU.ID, notification.TypeReview), "свой переход на проверку — без уведомления себе")
}

// §3.1: менеджеры проектов деактивированного получают список освобождённых задач своего проекта.
func TestDeactivationNotifiesManagers(t *testing.T) {
	f := newFixture(t)
	key := f.working(t)
	free := f.assigned(t)
	rec, _ := f.do(t, "PATCH", "/api/v1/users/"+f.memberU.ID.String(), f.admin, `{"is_active":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	f.process(t, events.UserDeactivated, "user_id", f.memberU.ID.String())
	notes := f.notes(t, f.managerU.ID, notification.TypeDeactivated)
	require.Len(t, notes, 1)
	require.Equal(t, f.p.Key, notes[0]["project_key"])
	require.Equal(t, "Пользователь", notes[0]["display_name"])
	require.ElementsMatch(t, []any{key, free}, notes[0]["released_tasks"])
	require.Empty(t, f.notes(t, f.memberU.ID, notification.TypeDeactivated))
}
