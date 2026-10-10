package notification_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

func newUser(t *testing.T, g *gorm.DB) *user.User {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), "", "Пользователь")
	require.NoError(t, g.Create(u).Error)
	return u
}

func count(t *testing.T, g *gorm.DB, u *user.User) int64 {
	t.Helper()
	var n int64
	require.NoError(t, g.Model(&notification.Notification{}).Where("user_id = ?", u.ID).Count(&n).Error)
	return n
}

func TestCreateIsIdempotentPerEvent(t *testing.T) {
	g := testutil.SharedDB(t)
	u := newUser(t, g)
	ev := uuid.New()
	for range 2 {
		require.NoError(t, notification.Create(context.Background(), g, notification.New{UserID: u.ID, Type: notification.TypeMemberAdded, EventID: &ev}))
	}
	require.EqualValues(t, 1, count(t, g, u), "повтор того же события — без дубля")
	require.NoError(t, notification.Create(context.Background(), g, notification.New{UserID: u.ID, Type: notification.TypeMemberAdded}))
	require.NoError(t, notification.Create(context.Background(), g, notification.New{UserID: u.ID, Type: notification.TypeMemberAdded}))
	require.EqualValues(t, 3, count(t, g, u), "без события — каждое отдельно")
}

func TestListReadAndCleanup(t *testing.T) {
	g := testutil.SharedDB(t)
	u, other := newUser(t, g), newUser(t, g)
	for range 5 {
		require.NoError(t, notification.Create(context.Background(), g, notification.New{UserID: u.ID, Type: notification.TypeMemberAdded, Payload: map[string]string{"project_key": "TM"}}))
	}
	require.NoError(t, notification.Create(context.Background(), g, notification.New{UserID: other.ID, Type: notification.TypeMemberAdded}))

	var ids []uuid.UUID
	cursor := ""
	for {
		page, err := notification.List(context.Background(), g, u.ID, false, 2, cursor, time.Now())
		require.NoError(t, err)
		if cursor == "" {
			require.EqualValues(t, 5, *page.UnreadCount)
		} else {
			require.Nil(t, page.UnreadCount, "счётчик — только на первой странице")
		}
		for _, n := range page.Items {
			ids = append(ids, n.ID)
			require.JSONEq(t, `{"project_key":"TM"}`, string(n.Payload))
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	require.Len(t, ids, 5, "все свои и только свои, без повторов")

	var foreign notification.Notification
	require.NoError(t, g.First(&foreign, "user_id = ?", other.ID).Error)
	n, err := notification.MarkRead(context.Background(), g, u.ID, []uuid.UUID{ids[0], ids[1], foreign.ID}, time.Now())
	require.NoError(t, err)
	require.EqualValues(t, 2, n, "чужое уведомление не отмечается")
	page, err := notification.List(context.Background(), g, u.ID, true, 0, "", time.Now())
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.EqualValues(t, 3, *page.UnreadCount)
	n, err = notification.MarkRead(context.Background(), g, u.ID, nil, time.Now())
	require.NoError(t, err)
	require.EqualValues(t, 3, n, "без ids — все")

	_, err = notification.List(context.Background(), g, u.ID, false, 0, "garbage!", time.Now())
	require.ErrorIs(t, err, notification.ErrBadCursor)
	_, err = notification.List(context.Background(), g, u.ID, false, 500, "", time.Now())
	require.ErrorIs(t, err, notification.ErrBadLimit)

	require.NoError(t, g.Exec(`UPDATE notifications SET created_at = now() - interval '31 days', read_at = NULL WHERE id = ?`, ids[0]).Error)
	page, err = notification.List(context.Background(), g, u.ID, false, 0, "", time.Now())
	require.NoError(t, err)
	require.Len(t, page.Items, 4, "старше 30 дней не показывается и до уборки")
	require.Zero(t, *page.UnreadCount, "и не считается непрочитанным")
	_, err = notification.Cleanup(context.Background(), g, time.Now())
	require.NoError(t, err)
	require.EqualValues(t, 4, count(t, g, u), "старше 30 дней удалено")
}

func envelope(t *testing.T, eventType string, payload map[string]any) *asynq.Task {
	t.Helper()
	p, err := json.Marshal(payload)
	require.NoError(t, err)
	b, err := json.Marshal(events.Envelope{EventID: uuid.NewString(), Type: eventType, Payload: p})
	require.NoError(t, err)
	return asynq.NewTask(events.TaskTypePrefix+eventType, b)
}

// §7.7: добавление в проект, исключение и смена роли видны в колокольчике; о своём действии — нет.
func TestMemberEventsCreateNotifications(t *testing.T) {
	g := testutil.SharedDB(t)
	d := events.NewDispatcher(nil)
	notification.Register(d, g)
	u, manager := newUser(t, g), newUser(t, g)
	pid := uuid.New()

	for _, et := range []string{events.MemberAdded, events.MemberRoleChanged, events.MemberRemoved} { // смена роли — не в колокольчик
		require.NoError(t, d.ProcessTask(context.Background(), envelope(t, et, map[string]any{
			"project_id": pid, "key": "TM", "user_id": u.ID, "role": "member", "old_role": "manager", "by": manager.ID,
		})))
	}
	require.NoError(t, d.ProcessTask(context.Background(), envelope(t, events.MemberAdded, map[string]any{
		"project_id": pid, "key": "TM", "user_id": manager.ID, "role": "manager", "by": manager.ID,
	})))

	page, err := notification.List(context.Background(), g, u.ID, false, 0, "", time.Now())
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "§7.7: добавление и исключение, без смены роли")
	require.Equal(t, notification.TypeMemberRemoved, page.Items[0].Type)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(page.Items[1].Payload, &payload))
	require.Equal(t, "TM", payload["project_key"])
	require.EqualValues(t, 0, count(t, g, manager), "о собственном действии не уведомляем")
}
