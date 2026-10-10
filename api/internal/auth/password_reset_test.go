package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

const newPassword = "brand new secret 42"

var linkRe = regexp.MustCompile(`https://\S+`)

// resetLink — работа воркера: выпускает ссылку и достаёт токен из письма.
func (f *fixture) resetLink(t *testing.T) string {
	t.Helper()
	before := len(f.mail.messages())
	require.NoError(t, f.svc.SendPasswordReset(context.Background(), f.u.Email))
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

// Новая ссылка не гасит прежние (иначе запросами восстановления можно «съедать» ссылку
// из полученного письма); после установки пароля гаснут все.
func TestSetPasswordCancelsAllOtherLinks(t *testing.T) {
	f := newFixture(t)
	first := f.resetLink(t)
	second := f.resetLink(t)
	third := f.resetLink(t)
	require.NoError(t, f.svc.SetPassword(context.Background(), second, newPassword))
	require.Equal(t, "token", fieldOf(t, f.svc.SetPassword(context.Background(), first, newPassword)))
	require.Equal(t, "token", fieldOf(t, f.svc.SetPassword(context.Background(), third, newPassword)))
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
	require.NoError(t, f.svc.SendPasswordReset(context.Background(), "nobody@example.com"))
	require.Empty(t, f.mail.messages())

	token := f.resetLink(t)
	require.NoError(t, f.db.Model(f.u).Update("is_active", false).Error)
	require.NoError(t, f.svc.SendPasswordReset(context.Background(), f.u.Email))
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

// pendingResets — задания восстановления в очереди для email.
func pendingResets(t *testing.T, email string) int {
	t.Helper()
	insp := asynq.NewInspector(queue.Opts(testutil.SharedRedisAddr(t)))
	defer func() { _ = insp.Close() }()
	tasks, err := insp.ListPendingTasks(queue.QueueDefault, asynq.PageSize(1000))
	if errors.Is(err, asynq.ErrQueueNotFound) {
		return 0
	}
	require.NoError(t, err)
	n := 0
	for _, tk := range tasks {
		if tk.Type == auth.TaskPasswordReset && strings.Contains(string(tk.Payload), `"`+email+`"`) {
			require.Equal(t, 10, tk.MaxRetry)
			n++
		}
	}
	return n
}

// API для любого email делает одно и то же — ставит задание воркеру; повтор в течение
// минуты задание не ставит.
func TestRequestPasswordResetEnqueuesWithCooldown(t *testing.T) {
	f := newFixture(t)
	unknown := testutil.UniqueEmail(t)
	for _, email := range []string{f.u.Email, unknown} {
		require.NoError(t, f.svc.RequestPasswordReset(context.Background(), email))
		require.NoError(t, f.svc.RequestPasswordReset(context.Background(), email))
		require.Equal(t, 1, pendingResets(t, email), email)
	}
	require.Empty(t, f.mail.messages(), "письмо ставит воркер, а не API")
}

func TestPasswordResetHandler(t *testing.T) {
	f := newFixture(t)
	payload, err := json.Marshal(map[string]string{"email": f.u.Email})
	require.NoError(t, err)
	h := auth.PasswordResetHandler(f.svc)
	require.NoError(t, h(context.Background(), asynq.NewTask(auth.TaskPasswordReset, payload)))
	require.Len(t, f.mail.messages(), 1)
	require.ErrorIs(t, h(context.Background(), asynq.NewTask(auth.TaskPasswordReset, []byte("{"))), asynq.SkipRetry)
}

func TestPasswordResetNeedsQueueAndMailer(t *testing.T) {
	f := newFixture(t)
	bare := auth.NewService(&config.Config{}, f.db, nil)
	require.Error(t, bare.RequestPasswordReset(context.Background(), f.u.Email))
	require.Error(t, bare.SendPasswordReset(context.Background(), f.u.Email))
}
