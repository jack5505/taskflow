package auth_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

const password = "correct horse battery"

type fixture struct {
	svc  *auth.Service
	mail *captureMailer
	db   *gorm.DB
	u    *user.User
	now  time.Time
	tick func(d time.Duration)
}

// newFixture — сервис с подменяемыми часами и свежий активный пользователь.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	g := testutil.SharedDB(t)
	rdb := queue.NewRedis(testutil.SharedRedisAddr(t))
	t.Cleanup(func() { _ = rdb.Close() })

	f := &fixture{db: g, now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	cfg := &config.Config{BaseURL: "https://tasks.example/", JWTSecret: "0123456789abcdef0123456789abcdef", JWTAccessTTL: 15 * time.Minute, JWTRefreshTTL: 30 * 24 * time.Hour}
	f.mail = &captureMailer{}
	jobs := queue.NewClient(testutil.SharedRedisAddr(t))
	t.Cleanup(func() { _ = jobs.Close() })
	f.svc = auth.NewService(cfg, g, rdb).WithClock(func() time.Time { return f.now }).WithMailer(f.mail).WithQueue(jobs)
	f.tick = func(d time.Duration) { f.now = f.now.Add(d) }

	hash, err := auth.HashPassword(password)
	require.NoError(t, err)
	f.u = user.New(testutil.UniqueEmail(t), hash, "Иван Петров")
	require.NoError(t, g.Create(f.u).Error)
	return f
}

func TestHashPassword(t *testing.T) {
	_, err := auth.HashPassword("short")
	require.ErrorIs(t, err, auth.ErrPasswordTooShort)
	_, err = auth.HashPassword(strings.Repeat("пароль", 7)) // 42 символа, 84 байта — за пределом bcrypt
	require.ErrorIs(t, err, auth.ErrPasswordTooLong)
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)
	require.True(t, auth.CheckPassword(hash, password))
	require.False(t, auth.CheckPassword(hash, password+"x"))
}

func TestLoginIssuesSessionAndAccessWorks(t *testing.T) {
	f := newFixture(t)
	s, err := f.svc.Login(context.Background(), f.u.Email, password, "test-agent")
	require.NoError(t, err)
	require.NotEmpty(t, s.AccessToken)
	require.NotEmpty(t, s.RefreshToken)
	require.Equal(t, 900, s.ExpiresIn)

	got, err := f.svc.Authenticate(context.Background(), s.AccessToken)
	require.NoError(t, err)
	require.Equal(t, f.u.ID, got.ID)

	// Через 15 минут access-токен мёртв, refresh ещё жив.
	f.tick(16 * time.Minute)
	_, err = f.svc.Authenticate(context.Background(), s.AccessToken)
	require.ErrorIs(t, err, auth.ErrNoToken)
	_, err = f.svc.Refresh(context.Background(), s.RefreshToken, "test-agent")
	require.NoError(t, err)
}

func TestLoginRejectsWrongPasswordUnknownEmailAndInactive(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Login(context.Background(), f.u.Email, "wrong password", "")
	require.ErrorIs(t, err, auth.ErrBadCredentials)
	_, err = f.svc.Login(context.Background(), "nobody-"+f.u.Email, password, "")
	require.ErrorIs(t, err, auth.ErrBadCredentials, "для неизвестного email тот же ответ")

	require.NoError(t, f.db.Model(f.u).Update("is_active", false).Error)
	_, err = f.svc.Login(context.Background(), f.u.Email, password, "")
	require.ErrorIs(t, err, auth.ErrUserInactive)
}

// После 10 неудачных попыток 11-я отклоняется даже с верным паролем (ТЗ §10.3).
func TestLockoutAfterTenFailures(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 10; i++ {
		_, err := f.svc.Login(context.Background(), f.u.Email, "wrong", "")
		require.ErrorIs(t, err, auth.ErrBadCredentials, "попытка %d", i+1)
	}
	_, err := f.svc.Login(context.Background(), f.u.Email, password, "")
	require.ErrorIs(t, err, auth.ErrLocked)
	e := apierr.From(err)
	require.Equal(t, 429, e.Status)
	retry, _ := e.Details["retry_after_seconds"].(int)
	require.Greater(t, retry, 0)
	require.LessOrEqual(t, retry, 15*60)
}

// Успешный вход сбрасывает счётчик: 9 неудач, успех, снова 9 неудач — блокировки нет.
func TestSuccessfulLoginResetsFailureCounter(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 9; i++ {
		_, _ = f.svc.Login(context.Background(), f.u.Email, "wrong", "")
	}
	_, err := f.svc.Login(context.Background(), f.u.Email, password, "")
	require.NoError(t, err)
	for i := 0; i < 9; i++ {
		_, _ = f.svc.Login(context.Background(), f.u.Email, "wrong", "")
	}
	_, err = f.svc.Login(context.Background(), f.u.Email, password, "")
	require.NoError(t, err)
}

func TestRefreshRotatesAndOldTokenDies(t *testing.T) {
	f := newFixture(t)
	s1, err := f.svc.Login(context.Background(), f.u.Email, password, "")
	require.NoError(t, err)

	s2, err := f.svc.Refresh(context.Background(), s1.RefreshToken, "")
	require.NoError(t, err)
	require.NotEqual(t, s1.RefreshToken, s2.RefreshToken)

	_, err = f.svc.Refresh(context.Background(), s1.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrBadRefresh, "использованный refresh отозван")
	_, err = f.svc.Refresh(context.Background(), "not-a-token", "")
	require.ErrorIs(t, err, auth.ErrBadRefresh)

	f.tick(31 * 24 * time.Hour)
	_, err = f.svc.Refresh(context.Background(), s2.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrBadRefresh, "через 30 дней refresh истёк")
}

// Критерий #9: после деактивации старый refresh не продлевает сессию, access тоже не работает.
// Украденный refresh: злоумышленник обновился первым, владелец пробует старый токен —
// повтор отозванного токена отзывает все сессии, включая злоумышленника.
func TestRefreshReuseRevokesAllSessions(t *testing.T) {
	f := newFixture(t)
	victim, err := f.svc.Login(context.Background(), f.u.Email, password, "victim")
	require.NoError(t, err)
	attacker, err := f.svc.Refresh(context.Background(), victim.RefreshToken, "attacker")
	require.NoError(t, err)

	_, err = f.svc.Refresh(context.Background(), victim.RefreshToken, "victim")
	require.ErrorIs(t, err, auth.ErrBadRefresh)
	_, err = f.svc.Refresh(context.Background(), attacker.RefreshToken, "attacker")
	require.ErrorIs(t, err, auth.ErrBadRefresh, "сессия злоумышленника отозвана вместе с остальными")
}

// Повтор токена, отозванного обычным logout (не ротацией), — не сигнал кражи:
// другие сессии пользователя трогать не нужно (ветка проверена в TestLogoutAndRevokeAll).
func TestRefreshAfterLogoutDoesNotRevokeOtherSessions(t *testing.T) {
	f := newFixture(t)
	s1, err := f.svc.Login(context.Background(), f.u.Email, password, "a")
	require.NoError(t, err)
	s2, err := f.svc.Login(context.Background(), f.u.Email, password, "b")
	require.NoError(t, err)

	require.NoError(t, f.svc.Logout(context.Background(), s1.RefreshToken))
	_, err = f.svc.Refresh(context.Background(), s1.RefreshToken, "a")
	require.ErrorIs(t, err, auth.ErrBadRefresh)
	_, err = f.svc.Refresh(context.Background(), s2.RefreshToken, "b")
	require.NoError(t, err, "logout чужого токена не должен отзывать эту сессию")
}

func TestDeactivationKillsRefreshAndAccess(t *testing.T) {
	f := newFixture(t)
	s, err := f.svc.Login(context.Background(), f.u.Email, password, "")
	require.NoError(t, err)

	require.NoError(t, f.db.Model(f.u).Update("is_active", false).Error)
	_, err = f.svc.Refresh(context.Background(), s.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrUserInactive)
	_, err = f.svc.Authenticate(context.Background(), s.AccessToken)
	require.ErrorIs(t, err, auth.ErrUserInactive, "деактивация действует сразу, не через 15 минут")
}

func TestLogoutAndRevokeAll(t *testing.T) {
	f := newFixture(t)
	s1, _ := f.svc.Login(context.Background(), f.u.Email, password, "a")
	s2, _ := f.svc.Login(context.Background(), f.u.Email, password, "b")
	s3, _ := f.svc.Login(context.Background(), f.u.Email, password, "c")

	require.NoError(t, f.svc.Logout(context.Background(), s1.RefreshToken))
	_, err := f.svc.Refresh(context.Background(), s1.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrBadRefresh)
	_, err = f.svc.Refresh(context.Background(), s2.RefreshToken, "")
	require.NoError(t, err, "logout одной сессии не трогает другие")
	require.NoError(t, f.svc.Logout(context.Background(), "unknown"), "неизвестный токен — не ошибка")

	require.NoError(t, f.svc.RevokeAll(context.Background(), f.u.ID))
	_, err = f.svc.Refresh(context.Background(), s3.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrBadRefresh, "«Выйти везде» отзывает все сессии")
}

func TestAuthenticateRejectsForeignAndMalformedTokens(t *testing.T) {
	f := newFixture(t)
	for _, tok := range []string{"", "garbage", "eyJhbGciOiJub25lIn0.eyJzdWIiOiJ4In0."} {
		_, err := f.svc.Authenticate(context.Background(), tok)
		require.ErrorIs(t, err, auth.ErrNoToken, tok)
	}
	rdb := queue.NewRedis(testutil.SharedRedisAddr(t))
	t.Cleanup(func() { _ = rdb.Close() })
	other := auth.NewService(&config.Config{JWTSecret: "another-secret-another-secret-xx", JWTAccessTTL: time.Minute, JWTRefreshTTL: time.Hour}, f.db, rdb)
	s, err := other.Login(context.Background(), f.u.Email, password, "")
	require.NoError(t, err)
	_, err = f.svc.Authenticate(context.Background(), s.AccessToken)
	require.ErrorIs(t, err, auth.ErrNoToken, "токен с другим секретом не принимается")
}

func TestCleanupExpired(t *testing.T) {
	f := newFixture(t)
	s, err := f.svc.Login(context.Background(), f.u.Email, password, "")
	require.NoError(t, err)
	n, err := f.svc.CleanupExpired(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "живой токен не удаляется")

	f.tick(31 * 24 * time.Hour)
	n, err = f.svc.CleanupExpired(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1))
	_, err = f.svc.Refresh(context.Background(), s.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrBadRefresh)
}

func ExampleHashPassword() {
	_, err := auth.HashPassword("short")
	fmt.Println(err)
	// Output: пароль короче 10 символов
}

// captureMailer запоминает письма вместо очереди: очередь проверяется в пакете mail.
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

func (c *captureMailer) messages() []mail.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]mail.Message(nil), c.sent...)
}
