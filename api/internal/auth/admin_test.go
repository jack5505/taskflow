package auth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

func TestEnsureSystemAdmin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	email := testutil.UniqueEmail(t)
	u, res, err := f.svc.EnsureSystemAdmin(ctx, email, "")
	require.NoError(t, err)
	require.Equal(t, auth.AdminCreated, res)
	require.True(t, u.IsSystemAdmin)
	require.True(t, u.IsActive)
	require.Equal(t, email[:strings.IndexByte(email, '@')], u.DisplayName, "имя по умолчанию — часть email до @")
	_, err = f.svc.Login(ctx, email, "", "")
	require.ErrorIs(t, err, auth.ErrBadCredentials, "до установки пароля войти нельзя")

	again, res, err := f.svc.EnsureSystemAdmin(ctx, strings.ToUpper(email[:1])+email[1:], "")
	require.NoError(t, err)
	require.Equal(t, auth.AdminExisted, res, "email без учёта регистра — тот же пользователь")
	require.Equal(t, u.ID, again.ID)

	promoted, res, err := f.svc.EnsureSystemAdmin(ctx, f.u.Email, "Другое имя")
	require.NoError(t, err)
	require.Equal(t, auth.AdminPromoted, res)
	require.True(t, promoted.IsSystemAdmin)
	require.Equal(t, f.u.DisplayName, promoted.DisplayName, "имя существующего пользователя не меняется")
	_, err = f.svc.Login(ctx, f.u.Email, password, "")
	require.NoError(t, err, "пароль существующего пользователя не сбрасывается")
}

func TestEnsureSystemAdminValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, email := range []string{"", "not-an-email", "Иван <ivan@example.com>", strings.Repeat("a", 250) + "@example.com"} {
		_, _, err := f.svc.EnsureSystemAdmin(ctx, email, "")
		require.ErrorIs(t, err, auth.ErrBadEmail, email)
	}
	_, _, err := f.svc.EnsureSystemAdmin(ctx, testutil.UniqueEmail(t), strings.Repeat("я", 81))
	require.ErrorIs(t, err, auth.ErrBadDisplayName)
	_, _, err = f.svc.EnsureSystemAdmin(ctx, testutil.UniqueEmail(t), "   ")
	require.ErrorIs(t, err, auth.ErrBadDisplayName)

	require.NoError(t, f.db.Model(f.u).Update("is_active", false).Error)
	_, _, err = f.svc.EnsureSystemAdmin(ctx, f.u.Email, "")
	require.ErrorIs(t, err, auth.ErrAdminInactive)
}

func TestSendPasswordSetLinkInvites(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.svc.SendPasswordSetLink(context.Background(), f.u))
	msgs := f.mail.messages()
	require.Len(t, msgs, 1)
	require.Equal(t, "TaskFlow: приглашение и установка пароля", msgs[0].Subject)
	token := strings.SplitN(linkRe.FindString(msgs[0].Text), "token=", 2)[1]
	require.NoError(t, f.svc.SetPassword(context.Background(), token, newPassword))

	var u user.User
	require.NoError(t, f.db.First(&u, "id = ?", f.u.ID).Error)
	require.True(t, auth.CheckPassword(u.PasswordHash, newPassword))
}

func TestEnsureSystemAdminTruncatesDefaultName(t *testing.T) {
	f := newFixture(t)
	local := strings.Repeat("a", 90) + testutil.UniqueKey(t)
	u, _, err := f.svc.EnsureSystemAdmin(context.Background(), local+"@example.com", "")
	require.NoError(t, err)
	require.Equal(t, local[:80], u.DisplayName)
}

func TestSendPasswordSetLinkWithoutMailer(t *testing.T) {
	f := newFixture(t)
	bare := auth.NewService(&config.Config{}, f.db, nil)
	require.Error(t, bare.SendPasswordSetLink(context.Background(), f.u))
	require.False(t, auth.HasPassword(&user.User{}))
	require.True(t, auth.HasPassword(f.u))
}
