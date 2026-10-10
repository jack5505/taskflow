package project_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

func createUser(t *testing.T, g *gorm.DB, email string) *user.User {
	t.Helper()
	u := &user.User{Email: email, PasswordHash: "x", DisplayName: "Анна Смирнова"}
	require.NoError(t, g.Create(u).Error)
	return u
}

func TestProjectDefaultsAndKeyRules(t *testing.T) {
	g := testutil.SharedDB(t)
	p := &project.Project{Key: "TM", Name: "Тайм-менеджмент"}
	require.NoError(t, g.Create(p).Error)

	var got project.Project
	require.NoError(t, g.First(&got, "id = ?", p.ID).Error)
	require.Equal(t, 0, got.TaskCounter)
	require.Equal(t, user.DefaultTimezone, got.Timezone)
	require.Equal(t, "10:00:00", got.DailySummaryTime, "сводка в 10:00 по умолчанию (§3.2)")
	require.False(t, got.IsArchived)
	require.Nil(t, got.DeletedAt)

	for _, bad := range []string{"tm", "T", "TOOLONGKEY1", "T-M", "ТМ"} {
		err := g.Create(&project.Project{Key: bad, Name: "x"}).Error
		require.Equal(t, "23514", pgCode(err), "ключ %q должен отклоняться правилом ^[A-Z]{2,10}$", bad)
	}
	err := g.Create(&project.Project{Key: "TM", Name: "дубль"}).Error
	require.True(t, errors.Is(err, gorm.ErrDuplicatedKey) || pgCode(err) == "23505", "ключ уникален: %v", err)
}

func TestMemberPairUniqueAndRoleChecked(t *testing.T) {
	g := testutil.SharedDB(t)
	u := createUser(t, g, "member@example.com")
	p := &project.Project{Key: "MB", Name: "Участники"}
	require.NoError(t, g.Create(p).Error)

	require.NoError(t, g.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: project.RoleManager}).Error)

	err := g.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: project.RoleMember}).Error
	require.True(t, errors.Is(err, gorm.ErrDuplicatedKey) || pgCode(err) == "23505", "пара project_id + user_id уникальна: %v", err)

	other := createUser(t, g, "member2@example.com")
	err = g.Create(&project.Member{ProjectID: p.ID, UserID: other.ID, Role: "admin"}).Error
	require.Equal(t, "23514", pgCode(err), "роль только manager или member")

	err = g.Create(&project.Member{ProjectID: uuid.New(), UserID: other.ID, Role: project.RoleMember}).Error
	require.Equal(t, "23503", pgCode(err), "ссылка на несуществующий проект")
}

func TestMembersDisappearWithProject(t *testing.T) {
	g := testutil.SharedDB(t)
	u := createUser(t, g, "cascade@example.com")
	p := &project.Project{Key: "CS", Name: "Каскад"}
	require.NoError(t, g.Create(p).Error)
	require.NoError(t, g.Create(&project.Member{ProjectID: p.ID, UserID: u.ID, Role: project.RoleMember}).Error)

	require.NoError(t, g.Exec("DELETE FROM projects WHERE id = ?", p.ID).Error)
	var n int64
	require.NoError(t, g.Model(&project.Member{}).Where("project_id = ?", p.ID).Count(&n).Error)
	require.Zero(t, n)
}

func TestValidRole(t *testing.T) {
	require.True(t, project.ValidRole(project.RoleManager))
	require.True(t, project.ValidRole(project.RoleMember))
	require.False(t, project.ValidRole("admin"))
}
