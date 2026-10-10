package apierr_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
)

func TestFromKeepsDomainErrorThroughWrapping(t *testing.T) {
	orig := apierr.Conflict(apierr.CodeLastManager, "Единственный менеджер").With("project", "TM")
	wrapped := errors.Join(errors.New("контекст"), orig)

	e := apierr.From(wrapped)
	require.Same(t, orig, e)
	require.Equal(t, http.StatusConflict, e.Status)
	require.Equal(t, "TM", e.Details["project"])
}

func TestFromTurnsUnknownErrorInto500(t *testing.T) {
	cause := errors.New("sql: connection reset")
	e := apierr.From(cause)
	require.Equal(t, http.StatusInternalServerError, e.Status)
	require.Equal(t, apierr.CodeInternal, e.Code)
	require.ErrorIs(t, e, cause, "причина сохраняется для лога")
	require.NotContains(t, e.Message, "sql", "сообщение клиенту фиксированное")
}

func TestConstructorsStatuses(t *testing.T) {
	cases := map[int]*apierr.Error{
		http.StatusBadRequest:            apierr.BadRequest(apierr.CodeInvalidDuration, "x"),
		http.StatusUnauthorized:          apierr.Unauthorized("x"),
		http.StatusForbidden:             apierr.Forbidden("x"),
		http.StatusNotFound:              apierr.NotFound("x"),
		http.StatusConflict:              apierr.Conflict(apierr.CodeVersionConflict, "x"),
		http.StatusRequestEntityTooLarge: apierr.PayloadTooLarge("x"),
		http.StatusTooManyRequests:       apierr.RateLimited("x"),
	}
	for status, e := range cases {
		require.Equal(t, status, e.Status, e.Code)
		require.NotEmpty(t, e.Error())
	}
}

func TestWithAndWrapDoNotMutateSentinel(t *testing.T) {
	sentinel := apierr.Conflict(apierr.CodeInvalidTransition, "Переход не разрешён")

	a := sentinel.With("from", "done")
	b := sentinel.With("from", "review").Wrap(errors.New("причина"))

	require.Nil(t, sentinel.Details, "sentinel остался без деталей")
	require.Equal(t, "done", a.Details["from"])
	require.Equal(t, "review", b.Details["from"])
	require.NoError(t, sentinel.Unwrap())
	require.ErrorIs(t, a, sentinel, "копия сравнивается с sentinel через errors.Is")
	require.ErrorIs(t, b, sentinel)
}
