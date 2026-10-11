package user_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/user"
)

func TestPersonalNotificationSettings(t *testing.T) {
	eff := user.EffectiveNotifications(nil)
	require.Len(t, eff, len(user.NotificationKeys()))
	for k, v := range eff {
		require.True(t, v, "по умолчанию все включены: %s", k)
	}
	stored, err := user.MergeNotifications(nil, map[string]bool{user.NotifyComment: false, user.NotifyDue: true})
	require.NoError(t, err)
	require.JSONEq(t, `{"comment":false}`, string(stored), "хранятся только выключенные")
	stored, err = user.MergeNotifications(stored, map[string]bool{user.NotifyComment: true})
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(stored))
	_, err = user.MergeNotifications(stored, map[string]bool{"bogus": false})
	require.Error(t, err)
	require.True(t, user.EffectiveNotifications(events.JSON("{bad"))[user.NotifyAssigned])
}
