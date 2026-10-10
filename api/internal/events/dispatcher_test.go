package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/events"
)

func task(t *testing.T, env events.Envelope) *asynq.Task {
	t.Helper()
	raw, err := json.Marshal(env)
	require.NoError(t, err)
	return asynq.NewTask(events.TaskTypePrefix+env.Type, raw)
}

func TestProcessTaskCallsSpecificAndWildcardHandlers(t *testing.T) {
	d := events.NewDispatcher(nil)
	var calls []string
	d.On(events.TaskCreated, "a", func(context.Context, events.Envelope) error { calls = append(calls, "a"); return nil })
	d.On("*", "all", func(context.Context, events.Envelope) error { calls = append(calls, "all"); return nil })
	d.On(events.TaskDeleted, "other", func(context.Context, events.Envelope) error { calls = append(calls, "other"); return nil })

	err := d.ProcessTask(context.Background(), task(t, events.Envelope{EventID: "e1", Type: events.TaskCreated, Payload: json.RawMessage(`{}`)}))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "all"}, calls)
}

func TestProcessTaskAggregatesHandlerErrors(t *testing.T) {
	d := events.NewDispatcher(nil)
	errA := errors.New("ошибка a")
	d.On(events.TaskCreated, "a", func(context.Context, events.Envelope) error { return errA })
	d.On(events.TaskCreated, "b", func(context.Context, events.Envelope) error { return errors.New("ошибка b") })

	err := d.ProcessTask(context.Background(), task(t, events.Envelope{EventID: "e2", Type: events.TaskCreated}))
	require.ErrorIs(t, err, errA)
	require.ErrorContains(t, err, "ошибка b")
	require.NotErrorIs(t, err, asynq.SkipRetry, "ошибки обработчиков повторяются")
}

func TestProcessTaskSkipsRetryOnBrokenEnvelope(t *testing.T) {
	d := events.NewDispatcher(nil)
	err := d.ProcessTask(context.Background(), asynq.NewTask(events.TaskTypePrefix+"x", []byte("{не json")))
	require.ErrorIs(t, err, asynq.SkipRetry)

	err = d.ProcessTask(context.Background(), task(t, events.Envelope{EventID: "", Type: events.TaskCreated}))
	require.ErrorIs(t, err, asynq.SkipRetry)
}

func TestProcessTaskWithoutHandlersSucceeds(t *testing.T) {
	d := events.NewDispatcher(nil)
	err := d.ProcessTask(context.Background(), task(t, events.Envelope{EventID: "e3", Type: events.SprintClosed}))
	require.NoError(t, err)
}

func TestOnPanicsOnDuplicateName(t *testing.T) {
	d := events.NewDispatcher(nil)
	d.On(events.TaskCreated, "dup", func(context.Context, events.Envelope) error { return nil })
	require.Panics(t, func() {
		d.On(events.TaskCreated, "dup", func(context.Context, events.Envelope) error { return nil })
	})
}

func TestMuxRoutesEventTasks(t *testing.T) {
	d := events.NewDispatcher(nil)
	called := false
	d.On(events.TaskCreated, "a", func(context.Context, events.Envelope) error { called = true; return nil })
	mux := d.Mux()
	err := mux.ProcessTask(context.Background(), task(t, events.Envelope{EventID: "e4", Type: events.TaskCreated}))
	require.NoError(t, err)
	require.True(t, called)
}

func TestKnownTypes(t *testing.T) {
	require.True(t, events.Known(events.TaskCreated))
	require.True(t, events.Known(events.ProjectDeleted))
	require.False(t, events.Known("task.craeted"))
	require.False(t, events.Known(""))
}

func TestJSONValueAndScan(t *testing.T) {
	var j events.JSON
	v, err := j.Value()
	require.NoError(t, err)
	require.Equal(t, "{}", v)

	require.NoError(t, j.Scan([]byte(`{"a":1}`)))
	v, err = j.Value()
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, v)

	require.NoError(t, j.Scan("[1]"))
	require.Equal(t, "[1]", string(j))

	require.NoError(t, j.Scan(nil))
	require.Nil(t, j)
	require.Error(t, j.Scan(42))

	out, err := json.Marshal(struct{ P events.JSON }{P: events.JSON(`{"x":true}`)})
	require.NoError(t, err)
	require.JSONEq(t, `{"P":{"x":true}}`, string(out))
	out, err = json.Marshal(struct{ P events.JSON }{})
	require.NoError(t, err)
	require.JSONEq(t, `{"P":null}`, string(out))

	var back struct{ P events.JSON }
	require.NoError(t, json.Unmarshal([]byte(`{"P":{"y":2}}`), &back))
	require.JSONEq(t, `{"y":2}`, string(back.P))
}
