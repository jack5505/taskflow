package user_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

func TestNewUserDefaults(t *testing.T) {
	g := testutil.SharedDB(t)
	u := user.New(testutil.UniqueEmail(t), "x", "Иван Петров")
	require.NoError(t, g.Create(u).Error)
	require.NotEqual(t, uuid.Nil, u.ID)

	var got user.User
	require.NoError(t, g.First(&got, "id = ?", u.ID).Error)
	require.Equal(t, model.DefaultTimezone, got.Timezone)
	require.True(t, got.IsActive)
	require.False(t, got.IsSystemAdmin)
	require.JSONEq(t, `{}`, string(got.NotificationSettings))
	require.Nil(t, got.TelegramUserID)
	require.False(t, got.CreatedAt.IsZero())
}

// Явное false при создании не подменяется умолчанием: у IsActive нет GORM-default.
func TestInactiveUserStaysInactive(t *testing.T) {
	g := testutil.SharedDB(t)
	u := user.New(testutil.UniqueEmail(t), "x", "Неактивный")
	u.IsActive = false
	require.NoError(t, g.Create(u).Error)
	var got user.User
	require.NoError(t, g.First(&got, "id = ?", u.ID).Error)
	require.False(t, got.IsActive)
}

// email уникален без учёта регистра (citext): Ivan@Example.com и ivan@example.com — один логин.
func TestEmailUniqueCaseInsensitive(t *testing.T) {
	g := testutil.SharedDB(t)
	email := testutil.UniqueEmail(t)
	require.NoError(t, g.Create(user.New(strings.ToUpper(email), "x", "А")).Error)
	err := g.Create(user.New(email, "x", "Б")).Error
	require.ErrorIs(t, err, gorm.ErrDuplicatedKey)

	var got user.User
	require.NoError(t, g.First(&got, "email = ?", strings.ToUpper(email)).Error, "поиск по email тоже без учёта регистра")
}

func TestTelegramUserIDUnique(t *testing.T) {
	g := testutil.SharedDB(t)
	tg := int64(100000) + testutil.Next()
	a := user.New(testutil.UniqueEmail(t), "x", "А")
	a.TelegramUserID = &tg
	require.NoError(t, g.Create(a).Error)

	b := user.New(testutil.UniqueEmail(t), "x", "Б")
	b.TelegramUserID = &tg
	require.ErrorIs(t, g.Create(b).Error, gorm.ErrDuplicatedKey, "один Telegram-аккаунт — один пользователь")

	// NULL в уникальной колонке не конфликтует: непривязанных пользователей может быть много.
	require.NoError(t, g.Create(user.New(testutil.UniqueEmail(t), "x", "В")).Error)
	require.NoError(t, g.Create(user.New(testutil.UniqueEmail(t), "x", "Г")).Error)
}

func TestDisplayNameLength(t *testing.T) {
	g := testutil.SharedDB(t)
	u := user.New(testutil.UniqueEmail(t), "x", "")
	require.ErrorIs(t, g.Create(u).Error, gorm.ErrCheckConstraintViolated, "пустое имя отклоняет база (§3.1: 1–80 символов)")
}
