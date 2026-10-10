package auth_test

import (
	"context"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
)

const newPassword = "brand new secret 42"

var linkRe = regexp.MustCompile(`https://\S+`)

// resetLink запрашивает восстановление и достаёт токен из последнего письма.
func (f *fixture) resetLink(t *testing.T) string {
	t.Helper()
	before := len(f.mail.messages())
	require.NoError(t, f.svc.RequestPasswordReset(context.Background(), f.u.Email))
	msgs := f.mail.messages()
	require.Len(t, msgs, before+1, "письмо со ссылкой отправлено")
	m := msgs[len(msgs)-1]
	require.Equal(t, f.u.Email, m.To)
	link := linkRe.FindString(m.Text)
	u, err := url.Parse(link)
	require.NoError(t, err)
	require.Equal(t, "tasks.example", u.Host)
	require.Equal(t, "/password-set", u.Path, "без двойного слэша после APP_BASE_URL")
	token := u.Query().Get("token")
	require.NotEmpty(t, token)
	return token
}

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	e := apierr.From(err)
	require.Equal(t, apierr.CodeValidation, e.Code)
	fields := e.Details["fields"].([]apierr.FieldError)
	require.Len(t, fields, 1)
	return fields[0].Field
}

// Критерий #10: ссылка работает один раз, повторное открытие отклоняется.
func TestPasswordLinkWorksOnce(t *testing.T) {
	f := newFixture(t)
	token := f.resetLink(t)

	require.NoError(t, f.svc.SetPassword(context.Background(), token, newPassword))
	_, err := f.svc.Login(context.Background(), f.u.Email, newPassword, "")
	require.NoError(t, err, "вход с новым паролем")
	_, err = f.svc.Login(context.Background(), f.u.Email, password, "")
	require.ErrorIs(t, err, auth.ErrBadCredentials, "старый пароль больше не подходит")

	err = f.svc.SetPassword(context.Background(), token, "another password 1")
	require.Equal(t, "token", fieldOf(t, err), "повторное открытие ссылки отклоняется")
}

func TestPasswordLinkExpiresAfter24Hours(t *testing.T) {
	f := newFixture(t)
	token := f.resetLink(t)
	f.tick(24*time.Hour + time.Second)
	err := f.svc.SetPassword(context.Background(), token, newPassword)
	require.Equal(t, "token", fieldOf(t, err))
}

func TestNewPasswordLinkCancelsPrevious(t *testing.T) {
	f := newFixture(t)
	first := f.resetLink(t)
	second := f.resetLink(t)
	require.Equal(t, "token", fieldOf(t, f.svc.SetPassword(context.Background(), first, newPassword)))
	require.NoError(t, f.svc.SetPassword(context.Background(), second, newPassword))
}

func TestSetPasswordValidatesLengthAndKeepsLink(t *testing.T) {
	f := newFixture(t)
	token := f.resetLink(t)
	err := f.svc.SetPassword(context.Background(), token, "short")
	require.Equal(t, "password", fieldOf(t, err), "пароль короче 10 символов не принимается")
	err = f.svc.SetPassword(context.Background(), token, "пароль пароль пароль пароль пароль пароль")
	require.Equal(t, "password", fieldOf(t, err), "длиннее 72 байт")
	require.NoError(t, f.svc.SetPassword(context.Background(), token, newPassword), "отказ по длине не гасит ссылку")
}

func TestSetPasswordUnknownToken(t *testing.T) {
	f := newFixture(t)
	require.Equal(t, "token", fieldOf(t, f.svc.SetPassword(context.Background(), "nope", newPassword)))
}

// §9.1: при смене пароля все сессии отзываются; блокировка входа снимается.
func TestSetPasswordRevokesSessionsAndClearsLockout(t *testing.T) {
	f := newFixture(t)
	s, err := f.svc.Login(context.Background(), f.u.Email, password, "")
	require.NoError(t, err)
	for range 10 {
		_, _ = f.svc.Login(context.Background(), f.u.Email, "wrong password", "")
	}
	_, err = f.svc.Login(context.Background(), f.u.Email, password, "")
	require.ErrorIs(t, err, auth.ErrLocked)

	require.NoError(t, f.svc.SetPassword(context.Background(), f.resetLink(t), newPassword))
	_, err = f.svc.Refresh(context.Background(), s.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrBadRefresh, "старая сессия отозвана")
	_, err = f.svc.Login(context.Background(), f.u.Email, newPassword, "")
	require.NoError(t, err, "после смены пароля блокировка снята")
}

func TestPasswordResetSilentForUnknownAndInactive(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.svc.RequestPasswordReset(context.Background(), "nobody@example.com"))
	require.Empty(t, f.mail.messages())

	token := f.resetLink(t)
	require.NoError(t, f.db.Model(f.u).Update("is_active", false).Error)
	require.NoError(t, f.svc.RequestPasswordReset(context.Background(), f.u.Email))
	require.Len(t, f.mail.messages(), 1, "деактивированному письмо не уходит")
	require.Equal(t, "token", fieldOf(t, f.svc.SetPassword(context.Background(), token, newPassword)),
		"деактивированный не может задать пароль по старой ссылке")
}

func TestCleanupRemovesExpiredPasswordLinks(t *testing.T) {
	f := newFixture(t)
	f.resetLink(t)
	f.tick(25 * time.Hour)
	_, err := f.svc.CleanupExpired(context.Background())
	require.NoError(t, err)
	var n int64
	require.NoError(t, f.db.Model(&auth.PasswordToken{}).Where("user_id = ?", f.u.ID).Count(&n).Error)
	require.Zero(t, n)
}
