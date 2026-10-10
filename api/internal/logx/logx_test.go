package logx_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/logx"
)

func TestFromReturnsDefaultWithoutLogger(t *testing.T) {
	require.Same(t, slog.Default(), logx.From(context.Background()))
}

func TestWithAndFromRoundTrip(t *testing.T) {
	l := slog.Default().With("request_id", "abc")
	ctx := logx.With(context.Background(), l)
	require.Same(t, l, logx.From(ctx))
}
