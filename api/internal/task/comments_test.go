package task_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

func TestComments(t *testing.T) {
	f := newFixture(t)
	other, otherToken := f.user(t, false)
	f.join(t, other, project.RoleMember)
	key := f.create(t, f.member, `{"title":"Обсуждаемая"}`)["key"].(string)
	base := "/api/v1/tasks/" + key + "/comments"

	rec, b := f.do(t, "POST", base, f.member, `{"body":"  **Первый** комментарий  "}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "**Первый** комментарий", b["body"])
	require.Equal(t, "web", b["source"])
	require.Equal(t, false, b["edited"])
	mine := b["id"].(string)
	rec, b = f.do(t, "POST", base, otherToken, `{"body":"Чужой"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	theirs := b["id"].(string)

	rec, _ = f.do(t, "GET", base, f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2)
	require.Equal(t, "Пользователь", list[0]["author_name"])
	require.EqualValues(t, 1, f.get(t, key).Version, "комментарий не меняет версию задачи")

	// Критерий #25: исполнитель не может удалить чужой комментарий.
	rec, b = f.do(t, "DELETE", "/api/v1/comments/"+theirs, f.member, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, apierr.CodeForbidden, errCode(b))
	rec, _ = f.do(t, "PATCH", "/api/v1/comments/"+theirs, f.member, `{"body":"Правлю чужое"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)

	rec, b = f.do(t, "PATCH", "/api/v1/comments/"+mine, f.member, `{"body":"Исправленный"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, true, b["edited"])
	rec, _ = f.do(t, "DELETE", "/api/v1/comments/"+theirs, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code, "менеджер удаляет чужое")
	rec, _ = f.do(t, "DELETE", "/api/v1/comments/"+theirs, f.manager, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	for body := range map[string]bool{`{"body":"   "}`: true, `{"body":"` + strings.Repeat("я", 5001) + `"}`: true} {
		rec, b = f.do(t, "POST", base, f.member, body)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, "body", field(t, b))
	}
	rec, _ = f.do(t, "GET", base, f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "POST", base, f.outsider, `{"body":"Посторонний"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "DELETE", "/api/v1/comments/"+mine, f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code, "чужой проект — 404")
	rec, _ = f.do(t, "DELETE", "/api/v1/comments/not-uuid", f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	var n int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'comment.created' AND payload->>'key' = ?`, key).Scan(&n).Error)
	require.EqualValues(t, 2, n)

	require.NoError(t, f.db.Model(f.p).Update("is_archived", true).Error)
	rec, b = f.do(t, "POST", base, f.member, `{"body":"В архиве"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, apierr.CodeProjectArchived, errCode(b))
}

// Комментарий из бота — source telegram (§3.6, §8.5).
func TestCommentFromBot(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"X"}`)["key"].(string)
	ctx := audit.WithSource(audit.WithActor(context.Background(), f.memberU.ID), audit.SourceBot)
	v, err := task.NewService(f.db, project.NewService(f.db)).AddComment(ctx, f.memberU, key, "Из Telegram")
	require.NoError(t, err)
	require.Equal(t, "telegram", v.Source)
}

// §7.7: «новый комментарий» — автору и исполнителю задачи, кроме автора комментария.
func TestCommentNotifies(t *testing.T) {
	f := newFixture(t)
	other, otherToken := f.user(t, false)
	f.join(t, other, project.RoleMember)
	key := f.create(t, f.manager, `{"title":"X","assignee_id":"`+other.ID.String()+`"}`)["key"].(string)
	rec, _ := f.do(t, "POST", "/api/v1/tasks/"+key+"/comments", otherToken, `{"body":"Вопрос по задаче"}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	var id string
	var payload []byte
	require.NoError(t, f.db.Raw(`SELECT id::text, payload::text::bytea FROM outbox_events WHERE type = 'comment.created' AND payload->>'key' = ?`, key).Row().Scan(&id, &payload))
	env, err := json.Marshal(events.Envelope{EventID: id, Type: events.CommentCreated, Payload: payload})
	require.NoError(t, err)
	d := events.NewDispatcher(nil)
	notification.Register(d, f.db)
	require.NoError(t, d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+events.CommentCreated, env)))

	var n int64
	require.NoError(t, f.db.Model(&notification.Notification{}).Where("user_id = ? AND type = 'comment'", f.managerU.ID).Count(&n).Error)
	require.EqualValues(t, 1, n, "автору задачи")
	require.NoError(t, f.db.Model(&notification.Notification{}).Where("user_id = ? AND type = 'comment'", other.ID).Count(&n).Error)
	require.Zero(t, n, "автору комментария — нет")
}
