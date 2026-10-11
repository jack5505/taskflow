package task_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/task"
)

func TestLabelsAndDateColumns(t *testing.T) {
	v, err := task.Labels(nil).Value()
	require.NoError(t, err)
	require.Equal(t, "[]", v, "nil — пустой массив, а не null")
	v, err = task.Labels{"ui", "бэкенд"}.Value()
	require.NoError(t, err)
	require.JSONEq(t, `["ui","бэкенд"]`, v.(string))

	var l task.Labels
	require.NoError(t, l.Scan([]byte(`["a","b"]`)))
	require.Equal(t, task.Labels{"a", "b"}, l)
	require.NoError(t, l.Scan(`["c"]`))
	require.Equal(t, task.Labels{"c"}, l)
	require.NoError(t, l.Scan(nil))
	require.Empty(t, l)
	require.Error(t, l.Scan(42))

	var d task.Date
	require.NoError(t, d.Scan(time.Date(2026, 10, 31, 15, 4, 5, 0, time.FixedZone("x", 3600))))
	b, err := json.Marshal(d)
	require.NoError(t, err)
	require.Equal(t, `"2026-10-31"`, string(b))
	dv, err := d.Value()
	require.NoError(t, err)
	require.Equal(t, "2026-10-31", dv)
	require.NoError(t, d.Scan("2026-01-02"))
	require.Equal(t, 2, d.Day())
	require.Error(t, d.Scan(3.14))
}
