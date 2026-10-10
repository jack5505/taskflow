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
