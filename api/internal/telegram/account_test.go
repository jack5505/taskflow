package telegram_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

func newUser(t *testing.T, g *gorm.DB, name string) *user.User {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), "hash", name)
	require.NoError(t, g.Create(u).Error)
	return u
}

func reload(t *testing.T, g *gorm.DB, id uuid.UUID) *user.User {
	t.Helper()
	var u user.User
	require.NoError(t, g.First(&u, "id = ?", id).Error)
	return &u
}

func tgID() int64 { return 700_000_000 + testutil.Next() }

// §8.1: код из 8 символов (латиница и цифры), 10 минут, ссылка https://t.me/<бот>?start=<код>.
func TestIssueLinkCode(t *testing.T) {
	g := testutil.SharedDB(t)
	a := telegram.NewAccounts(g, "@taskflow_bot")
	u := newUser(t, g, "Иван Петров")

	c, err := a.IssueLinkCode(context.Background(), u.ID)
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`^[A-Z0-9]{8}$`), c.Code)
	require.WithinDuration(t, time.Now().Add(10*time.Minute), c.ExpiresAt, 5*time.Second)
	require.Equal(t, "https://t.me/taskflow_bot?start="+c.Code, c.URL)

	// Новый код гасит прежний: действует только последний показанный.
	c2, err := a.IssueLinkCode(context.Background(), u.ID)
	require.NoError(t, err)
	require.NotEqual(t, c.Code, c2.Code)
	_, err = a.Link(context.Background(), c.Code, tgID(), "ivan")
	require.ErrorIs(t, err, telegram.ErrCodeInvalid)

	noBot, err := telegram.NewAccounts(g, "").IssueLinkCode(context.Background(), u.ID)
	require.NoError(t, err)
	require.Empty(t, noBot.URL, "без имени бота ссылки нет, код — есть")
}

func TestLink(t *testing.T) {
	g := testutil.SharedDB(t)
	a := telegram.NewAccounts(g, "taskflow_bot")
	ctx := context.Background()
	u := newUser(t, g, "Иван Петров")
	tg := tgID()

	c, err := a.IssueLinkCode(ctx, u.ID)
	require.NoError(t, err)
	linked, err := a.Link(ctx, " "+strings.ToLower(c.Code)+" ", tg, "ivan")
	require.NoError(t, err, "регистр и пробелы при ручном вводе не важны")
	require.Equal(t, u.ID, linked.ID)
	got := reload(t, g, u.ID)
	require.Equal(t, tg, *got.TelegramUserID)
	require.Equal(t, "ivan", *got.TelegramUsername)

	_, err = a.Link(ctx, c.Code, tg, "ivan")
	require.ErrorIs(t, err, telegram.ErrCodeInvalid, "код одноразовый")
	_, err = a.Link(ctx, "NOPE1234", tg, "ivan")
	require.ErrorIs(t, err, telegram.ErrCodeInvalid)

	// Повторная привязка того же Telegram к тому же пользователю — без ошибки; без username — NULL.
	c, err = a.IssueLinkCode(ctx, u.ID)
	require.NoError(t, err)
	_, err = a.Link(ctx, c.Code, tg, "")
	require.NoError(t, err)
	require.Nil(t, reload(t, g, u.ID).TelegramUsername)
}

func TestLinkExpiredAndInactive(t *testing.T) {
	g := testutil.SharedDB(t)
	a := telegram.NewAccounts(g, "taskflow_bot")
	ctx := context.Background()
	u := newUser(t, g, "Анна")

	c, err := a.IssueLinkCode(ctx, u.ID)
	require.NoError(t, err)
	a.SetClock(func() time.Time { return time.Now().Add(10 * time.Minute) })
	_, err = a.Link(ctx, c.Code, tgID(), "anna")
	require.ErrorIs(t, err, telegram.ErrCodeInvalid, "ровно через 10 минут код истёк")
	a.SetClock(time.Now)

	require.NoError(t, g.Model(u).Update("is_active", false).Error)
	_, err = a.Link(ctx, c.Code, tgID(), "anna")
	require.ErrorIs(t, err, telegram.ErrCodeInvalid, "деактивированный не привязывается")
}

// §8.1 п. 5: Telegram уже привязан к другому — отказ, код не гасится; после /unlink им можно
// воспользоваться.
func TestLinkToOtherAccount(t *testing.T) {
	g := testutil.SharedDB(t)
	a := telegram.NewAccounts(g, "taskflow_bot")
	ctx := context.Background()
	first, second := newUser(t, g, "Первый"), newUser(t, g, "Второй")
	tg := tgID()

	c1, err := a.IssueLinkCode(ctx, first.ID)
	require.NoError(t, err)
	_, err = a.Link(ctx, c1.Code, tg, "shared")
	require.NoError(t, err)

	c2, err := a.IssueLinkCode(ctx, second.ID)
	require.NoError(t, err)
	_, err = a.Link(ctx, c2.Code, tg, "shared")
	require.ErrorIs(t, err, telegram.ErrLinkedToOther)
	require.Nil(t, reload(t, g, second.ID).TelegramUserID)

	require.NoError(t, a.Unlink(ctx, first.ID))
	require.Nil(t, reload(t, g, first.ID).TelegramUserID)
	require.NoError(t, a.Unlink(ctx, first.ID), "повторная отвязка — без ошибки")
	_, err = a.Link(ctx, c2.Code, tg, "shared")
	require.NoError(t, err, "код не был погашен отказом")
	require.Equal(t, tg, *reload(t, g, second.ID).TelegramUserID)
}

// Один Telegram одновременно привязывают к двум аккаунтам — успевает ровно один.
func TestLinkConcurrent(t *testing.T) {
	g := testutil.SharedDB(t)
	a := telegram.NewAccounts(g, "taskflow_bot")
	ctx := context.Background()
	tg := tgID()
	var codes []string
	for range 4 {
		c, err := a.IssueLinkCode(ctx, newUser(t, g, "Гонка").ID)
		require.NoError(t, err)
		codes = append(codes, c.Code)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(codes))
	for i, code := range codes {
		wg.Go(func() { _, errs[i] = a.Link(ctx, code, tg, "race") })
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		require.ErrorIs(t, err, telegram.ErrLinkedToOther)
	}
	require.Equal(t, 1, ok)
	var n int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM users WHERE telegram_user_id = ?`, tg).Scan(&n).Error)
	require.EqualValues(t, 1, n)
}

// §3.1: telegram_username обновляется при каждом сообщении боту.
func TestByTelegram(t *testing.T) {
	g := testutil.SharedDB(t)
	a := telegram.NewAccounts(g, "taskflow_bot")
	ctx := context.Background()
	u := newUser(t, g, "Пётр")
	tg := tgID()
	c, err := a.IssueLinkCode(ctx, u.ID)
	require.NoError(t, err)
	_, err = a.Link(ctx, c.Code, tg, "old")
	require.NoError(t, err)

	before := reload(t, g, u.ID).UpdatedAt
	got, err := a.ByTelegram(ctx, tg, "new")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)
	require.Equal(t, "new", *reload(t, g, u.ID).TelegramUsername)
	require.Equal(t, before, reload(t, g, u.ID).UpdatedAt, "служебное поле не меняет updated_at профиля")
	_, err = a.ByTelegram(ctx, tg, "")
	require.NoError(t, err)
	require.Nil(t, reload(t, g, u.ID).TelegramUsername, "username убрали в Telegram — убираем и у нас")

	none, err := a.ByTelegram(ctx, tgID(), "x")
	require.NoError(t, err)
	require.Nil(t, none)
	require.NoError(t, g.Model(u).Update("is_active", false).Error)
	none, err = a.ByTelegram(ctx, tg, "")
	require.NoError(t, err)
	require.Nil(t, none, "деактивированный — как непривязанный")
}

func TestCleanupCodes(t *testing.T) {
	g := testutil.SharedDB(t)
	a := telegram.NewAccounts(g, "taskflow_bot")
	ctx := context.Background()
	u := newUser(t, g, "Уборка")
	c, err := a.IssueLinkCode(ctx, u.ID)
	require.NoError(t, err)

	_, err = a.CleanupCodes(ctx)
	require.NoError(t, err)
	var left int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM one_time_codes WHERE code = ?`, c.Code).Scan(&left).Error)
	require.EqualValues(t, 1, left, "живой код не трогаем")

	a.SetClock(func() time.Time { return time.Now().Add(25 * time.Hour) })
	n, err := a.CleanupCodes(ctx)
	require.NoError(t, err)
	require.Positive(t, n)
	require.NoError(t, g.Raw(`SELECT count(*) FROM one_time_codes WHERE code = ?`, c.Code).Scan(&left).Error)
	require.Zero(t, left)
}

// Два одновременных запроса кода — действует только один (новый гасит прежний).
func TestIssueLinkCodeConcurrent(t *testing.T) {
	g := testutil.SharedDB(t)
	a := telegram.NewAccounts(g, "taskflow_bot")
	u := newUser(t, g, "Две вкладки")
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_, err := a.IssueLinkCode(context.Background(), u.ID)
			require.NoError(t, err)
		})
	}
	wg.Wait()
	var n int64
	require.NoError(t, g.Raw(`SELECT count(*) FROM one_time_codes WHERE user_id = ? AND used_at IS NULL`, u.ID).Scan(&n).Error)
	require.EqualValues(t, 1, n)
}
