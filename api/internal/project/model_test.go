package project_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

func createUser(t *testing.T, g *gorm.DB) *user.User {
	t.Helper()
	u := user.New(testutil.UniqueEmail(t), "x", "Анна Смирнова")
	require.NoError(t, g.Create(u).Error)
	return u
}

func TestNewProjectDefaults(t *testing.T) {
	g := testutil.SharedDB(t)
	p := project.New(testutil.UniqueKey(t), "Тайм-менеджмент")
	require.NoError(t, g.Create(p).Error)
	require.Equal(t, project.DefaultDailySummaryTime, p.DailySummaryTime)

	var got project.Project
	require.NoError(t, g.First(&got, "id = ?", p.ID).Error)
	require.Equal(t, 0, got.TaskCounter)
	require.Equal(t, model.DefaultTimezone, got.Timezone)
	require.Equal(t, project.DefaultDailySummaryTime, got.DailySummaryTime, "после чтения из базы то же «10:00», что и после Create")
	require.False(t, got.IsArchived)
	require.False(t, got.DeletedAt.Valid)
}

func TestKeyRulesAndUniqueness(t *testing.T) {
	g := testutil.SharedDB(t)
	for _, bad := range []string{"tm", "T", "TOOLONGKEY1", "T-M", "ТМ"} {
		err := g.Create(project.New(bad, "x")).Error
		require.ErrorIs(t, err, gorm.ErrCheckConstraintViolated, "ключ %q должен отклоняться правилом ^[A-Z]{2,10}$", bad)
	}
	key := testutil.UniqueKey(t)
	require.NoError(t, g.Create(project.New(key, "первый")).Error)
	require.ErrorIs(t, g.Create(project.New(key, "дубль")).Error, gorm.ErrDuplicatedKey)
}

// Мягко удалённый проект исчезает из обычных запросов сам (gorm.DeletedAt) и виден только через Unscoped.
func TestSoftDeletedProjectIsHiddenByDefault(t *testing.T) {
	g := testutil.SharedDB(t)
	p := project.New(testutil.UniqueKey(t), "Удаляемый")
	require.NoError(t, g.Create(p).Error)
	require.NoError(t, g.Delete(p).Error)

	var n int64
	require.NoError(t, g.Model(&project.Project{}).Where("id = ?", p.ID).Count(&n).Error)
	require.Zero(t, n, "обычный запрос не видит удалённый проект")
	require.NoError(t, g.Unscoped().Model(&project.Project{}).Where("id = ?", p.ID).Count(&n).Error)
	require.EqualValues(t, 1, n, "строка осталась для корзины и восстановления (§3.2)")
}

func TestMemberPairUniqueAndRoleChecked(t *testing.T) {
	g := testutil.SharedDB(t)
	u := createUser(t, g)
	p := project.New(testutil.UniqueKey(t), "Участники")
	require.NoError(t, g.Create(p).Error)

	require.NoError(t, g.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: project.RoleManager}).Error)
	err := g.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: project.RoleMember}).Error
	require.ErrorIs(t, err, gorm.ErrDuplicatedKey, "пара project_id + user_id уникальна")

	other := createUser(t, g)
	err = g.Create(&project.Member{ProjectID: p.ID, UserID: other.ID, Role: "admin"}).Error
	require.ErrorIs(t, err, gorm.ErrCheckConstraintViolated, "роль только manager или member")

	err = g.Create(&project.Member{ProjectID: uuid.New(), UserID: other.ID, Role: project.RoleMember}).Error
	require.ErrorIs(t, err, gorm.ErrForeignKeyViolated, "ссылка на несуществующий проект")
}

func TestMembersDisappearWithProject(t *testing.T) {
	g := testutil.SharedDB(t)
	u := createUser(t, g)
	p := project.New(testutil.UniqueKey(t), "Каскад")
	require.NoError(t, g.Create(p).Error)
	require.NoError(t, g.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: project.RoleMember}).Error)

	require.NoError(t, g.Unscoped().Delete(p).Error, "физическое удаление строки")
	var n int64
	require.NoError(t, g.Model(&project.Member{}).Where("project_id = ?", p.ID).Count(&n).Error)
	require.Zero(t, n)
}

func TestValidRole(t *testing.T) {
	require.True(t, project.ValidRole(project.RoleManager))
	require.True(t, project.ValidRole(project.RoleMember))
	require.False(t, project.ValidRole("admin"))
}
