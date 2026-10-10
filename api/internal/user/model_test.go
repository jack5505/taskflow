package user_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

func newUser(email string) *user.User {
	return &user.User{Email: email, PasswordHash: "x", DisplayName: "Иван Петров"}
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.Is(err, gorm.ErrDuplicatedKey) || (errors.As(err, &pg) && pg.Code == "23505")
}

func isCheckViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23514"
}

func TestUserDefaultsFromSchema(t *testing.T) {
	g := testutil.SharedDB(t)
	u := newUser("defaults@example.com")
	require.NoError(t, g.Create(u).Error)
	require.NotEqual(t, uuid.Nil, u.ID)

	var got user.User
	require.NoError(t, g.First(&got, "id = ?", u.ID).Error)
	require.Equal(t, user.DefaultTimezone, got.Timezone)
	require.True(t, got.IsActive)
	require.False(t, got.IsSystemAdmin)
	require.JSONEq(t, `{}`, string(got.NotificationSettings))
	require.Nil(t, got.TelegramUserID)
	require.False(t, got.CreatedAt.IsZero())
}

// email уникален без учёта регистра (citext): Ivan@Example.com и ivan@example.com — один логин.
func TestEmailUniqueCaseInsensitive(t *testing.T) {
	g := testutil.SharedDB(t)
	require.NoError(t, g.Create(newUser("Ivan@Example.com")).Error)
	err := g.Create(newUser("ivan@example.com")).Error
	require.Error(t, err)
	require.True(t, isUniqueViolation(err), "ожидалось нарушение уникальности, получено: %v", err)

	var got user.User
	require.NoError(t, g.First(&got, "email = ?", "IVAN@EXAMPLE.COM").Error, "поиск по email тоже без учёта регистра")
}

func TestTelegramUserIDUnique(t *testing.T) {
	g := testutil.SharedDB(t)
	tg := int64(123456789)
	a := newUser("tg-a@example.com")
	a.TelegramUserID = &tg
	require.NoError(t, g.Create(a).Error)

	b := newUser("tg-b@example.com")
	b.TelegramUserID = &tg
	err := g.Create(b).Error
	require.True(t, isUniqueViolation(err), "один Telegram-аккаунт — один пользователь: %v", err)

	// NULL в уникальной колонке не конфликтует: непривязанных пользователей может быть много.
	require.NoError(t, g.Create(newUser("tg-c@example.com")).Error)
	require.NoError(t, g.Create(newUser("tg-d@example.com")).Error)
}

func TestDisplayNameLength(t *testing.T) {
	g := testutil.SharedDB(t)
	u := newUser("name@example.com")
	u.DisplayName = ""
	require.True(t, isCheckViolation(g.Create(u).Error), "пустое имя отклоняет база (§3.1: 1–80 символов)")
}
