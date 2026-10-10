package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

type captureMailer struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (c *captureMailer) Send(_ context.Context, m mail.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m)
	return nil
}

var tokenRe = regexp.MustCompile(`/password-set\?token=(\S+)`)

func newAdminService(t *testing.T) (*auth.Service, *captureMailer) {
	t.Helper()
	cfg := &config.Config{BaseURL: "https://tasks.example", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: time.Minute, JWTRefreshTTL: time.Hour}
	m := &captureMailer{}
	rdb := queue.NewRedis(testutil.SharedRedisAddr(t))
	t.Cleanup(func() { _ = rdb.Close() })
	return auth.NewService(cfg, testutil.SharedDB(t), rdb).WithMailer(m), m
}

// Критерий #11: без SMTP ссылка печатается; повторный вызов не создаёт дубликат.
func TestAdminCreatePrintsLinkWithoutSMTPAndIsIdempotent(t *testing.T) {
	svc, m := newAdminService(t)
	email := testutil.UniqueEmail(t)

	var out bytes.Buffer
	require.NoError(t, adminCreate(context.Background(), svc, email, "", false, &out))
	require.Contains(t, out.String(), "Администратор "+email+" создан.")
	first := tokenRe.FindStringSubmatch(out.String())
	require.Len(t, first, 2, out.String())
	require.Empty(t, m.sent, "без SMTP письмо не ставится")

	out.Reset()
	require.NoError(t, adminCreate(context.Background(), svc, email, "", false, &out))
	require.Contains(t, out.String(), "новый не создан")
	second := tokenRe.FindStringSubmatch(out.String())
	require.Len(t, second, 2)

	var n int64
	require.NoError(t, testutil.SharedDB(t).Model(&user.User{}).Where("email = ?", email).Count(&n).Error)
	require.EqualValues(t, 1, n, "дубликата нет")

	require.NoError(t, svc.SetPassword(context.Background(), first[1], "admin password 1"), "первая ссылка тоже действует")
	s, err := svc.Login(context.Background(), email, "admin password 1", "")
	require.NoError(t, err, "администратор входит с заданным паролем")
	require.True(t, s.User.IsSystemAdmin)
}

func TestAdminCreateWithSMTPQueuesInvite(t *testing.T) {
	svc, m := newAdminService(t)
	email := testutil.UniqueEmail(t)
	var out bytes.Buffer
	require.NoError(t, adminCreate(context.Background(), svc, email, "Анна Смирнова", true, &out))
	require.Contains(t, out.String(), "поставлено в очередь")
	require.NotContains(t, out.String(), "token=", "при SMTP ссылка в консоль не печатается")
	require.Len(t, m.sent, 1)
	require.Equal(t, email, m.sent[0].To)
	require.Contains(t, m.sent[0].Text, "Анна Смирнова")
	require.True(t, strings.Contains(m.sent[0].Text, "https://tasks.example/password-set?token="))
}

func TestAdminCreateErrors(t *testing.T) {
	svc, _ := newAdminService(t)
	require.ErrorIs(t, adminCreate(context.Background(), svc, "not-an-email", "", false, &bytes.Buffer{}), auth.ErrBadEmail)
	require.ErrorIs(t, runAdmin(context.Background(), &config.Config{}, nil), errAdminUsage)
	require.ErrorIs(t, runAdmin(context.Background(), &config.Config{}, []string{"create"}), errAdminUsage)
	require.ErrorIs(t, runAdmin(context.Background(), &config.Config{}, []string{"delete", "--email", "a@b.c"}), errAdminUsage)
}
