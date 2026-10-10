package model_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/model"
)

func TestBeforeCreateAssignsUUIDOnce(t *testing.T) {
	var b model.Base
	require.NoError(t, b.BeforeCreate(nil))
	require.NotEqual(t, uuid.Nil, b.ID)

	fixed := b.ID
	require.NoError(t, b.BeforeCreate(nil))
	require.Equal(t, fixed, b.ID, "заданный id не перезаписывается")
}

func TestTimeOfDayRoundTrip(t *testing.T) {
	var tod model.TimeOfDay
	require.NoError(t, tod.Scan("10:00:00"))
	require.Equal(t, model.TimeOfDay("10:00"), tod)
	require.NoError(t, tod.Scan([]byte("09:05")))
	require.Equal(t, "09:05", tod.String())

	v, err := model.TimeOfDay("10:00").Value()
	require.NoError(t, err)
	require.Equal(t, "10:00:00", v)
	_, err = model.TimeOfDay("25:99").Value()
	require.Error(t, err)
	require.Error(t, tod.Scan(42))
	require.NoError(t, tod.Scan(nil))
	require.Empty(t, tod)
}
