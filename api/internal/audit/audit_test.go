package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/testutil"
	"github.com/jack5505/taskflow/api/internal/user"
)

func setup(t *testing.T) (*gorm.DB, *user.User, *project.Project) {
	t.Helper()
	g := testutil.SharedDB(t)
	u := user.New(testutil.UniqueEmail(t), "", "Админ")
	require.NoError(t, g.Create(u).Error)
	p := project.New(testutil.UniqueKey(t), "Проект")
	require.NoError(t, g.Create(p).Error)
	return g, u, p
}

func write(t *testing.T, g *gorm.DB, ctx context.Context, r audit.Record) {
	t.Helper()
	require.NoError(t, g.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return audit.Write(tx, r) }))
}

func TestWriteTakesActorAndSourceFromContext(t *testing.T) {
	g, u, p := setup(t)
	ctx := audit.WithActor(context.Background(), u.ID)
	write(t, g, ctx, audit.Record{ProjectID: &p.ID, Action: audit.MemberAdded, EntityType: "user", EntityID: "x", New: map[string]string{"role": "member"}})
	write(t, g, audit.WithSource(context.Background(), audit.SourceCLI), audit.Record{Action: audit.UserCreated, EntityType: "user", EntityID: "y"})
	write(t, g, context.Background(), audit.Record{Action: audit.UserUpdated, EntityType: "user", EntityID: "z"})

	var e audit.Entry
	require.NoError(t, g.First(&e, "project_id = ?", p.ID).Error)
	require.Equal(t, u.ID, *e.ActorID)
	require.Equal(t, audit.SourceWeb, e.Source, "по умолчанию — web")
	require.Nil(t, e.OldValue, "нет прежнего значения — NULL, а не {}")
	require.JSONEq(t, `{"role":"member"}`, string(*e.NewValue))

	var cli audit.Entry
	require.NoError(t, g.First(&cli, "entity_id = 'y'").Error)
	require.Nil(t, cli.ActorID)
	require.Nil(t, cli.ProjectID)
	require.Equal(t, audit.SourceCLI, cli.Source)

	var sys audit.Entry
	require.NoError(t, g.First(&sys, "entity_id = 'z'").Error)
	require.Equal(t, audit.SourceSystem, sys.Source, "без актора и источника — system, а не web")
}

// §3.8: журнал не редактируется — UPDATE и DELETE отклоняет база.
func TestEntriesAreImmutable(t *testing.T) {
	g, _, p := setup(t)
	write(t, g, context.Background(), audit.Record{ProjectID: &p.ID, Action: audit.ProjectUpdated, EntityType: "project", EntityID: p.ID.String()})
	require.ErrorContains(t, g.Exec(`UPDATE audit_logs SET action = 'x' WHERE project_id = ?`, p.ID).Error, "не изменяются")
	require.ErrorContains(t, g.Exec(`DELETE FROM audit_logs WHERE project_id = ?`, p.ID).Error, "не изменяются")
}

func TestListFiltersAndPages(t *testing.T) {
	g, u, p := setup(t)
	other := user.New(testutil.UniqueEmail(t), "", "Другой")
	require.NoError(t, g.Create(other).Error)
	for i := range 5 {
		actor := u.ID
		if i == 4 {
			actor = other.ID
		}
		action := audit.MemberAdded
		if i%2 == 1 {
			action = audit.MemberRemoved
		}
		write(t, g, audit.WithActor(context.Background(), actor), audit.Record{ProjectID: &p.ID, Action: action, EntityType: "user", EntityID: uuid.NewString()})
	}

	var seen []uuid.UUID
	cursor := ""
	for range 3 {
		page, err := audit.List(context.Background(), g, p.ID, audit.Filter{Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, e := range page.Items {
			seen = append(seen, e.ID)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	require.Len(t, seen, 5, "все записи ровно по разу")
	require.Equal(t, seen[0], mustLatest(t, g, p.ID), "новые первыми")
	require.Empty(t, cursor, "на последней странице курсора нет")

	page, err := audit.List(context.Background(), g, p.ID, audit.Filter{Action: audit.MemberRemoved})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	page, err = audit.List(context.Background(), g, p.ID, audit.Filter{ActorID: &other.ID})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	future := time.Now().Add(time.Hour)
	page, err = audit.List(context.Background(), g, p.ID, audit.Filter{From: &future})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	page, err = audit.List(context.Background(), g, p.ID, audit.Filter{To: &future})
	require.NoError(t, err)
	require.Len(t, page.Items, 5)

	_, err = audit.List(context.Background(), g, p.ID, audit.Filter{Cursor: "%%%"})
	require.ErrorIs(t, err, audit.ErrBadCursor)
}

func mustLatest(t *testing.T, g *gorm.DB, projectID uuid.UUID) uuid.UUID {
	t.Helper()
	var id string
	require.NoError(t, g.Raw(`SELECT id::text FROM audit_logs WHERE project_id = ? ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&id).Error)
	return uuid.MustParse(id)
}

// Записи одной транзакции получают разное время (clock_timestamp) и идут в порядке действий.
func TestEntriesOfOneTransactionKeepOrder(t *testing.T) {
	g, _, p := setup(t)
	require.NoError(t, g.Transaction(func(tx *gorm.DB) error {
		for _, a := range []string{audit.ProjectUpdated, audit.ProjectArchived} {
			if err := audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: a, EntityType: "project", EntityID: p.ID.String()}); err != nil {
				return err
			}
		}
		return nil
	}))
	page, err := audit.List(context.Background(), g, p.ID, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Equal(t, audit.ProjectArchived, page.Items[0].Action)
	require.Equal(t, audit.ProjectUpdated, page.Items[1].Action)
	require.True(t, page.Items[0].CreatedAt.After(page.Items[1].CreatedAt))
}
