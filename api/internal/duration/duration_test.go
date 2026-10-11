package duration_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/duration"
)

// Примеры §6.1 и границы формата.
func TestParse(t *testing.T) {
	ok := map[string]int{
		"2h 30m":    150,
		"45m":       45,
		"3h":        180,
		"1d":        480,
		"2h30m":     150, // пробелы между частями необязательны
		"1d 2h 30m": 630,
		"1d2h30m":   630,
		"1d 30m":    510,
		" 1h  30m ": 90,
		"90m":       90,
		"0m":        0,
		"3d":        1440,
		"99999m":    99999,
		"1h\t15m":   75,
	}
	for in, want := range ok {
		got, err := duration.Parse(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, in := range []string{
		"", "   ", "2", "h", "30m 2h", "2h 1d", "1h 1h", "1.5h", "2,5h", "-1h", "2 h", "2H", "1w",
		"2h30", "abc", "1d 2h 30m 10s", "100000m", "2h-30m",
	} {
		_, err := duration.Parse(in)
		require.ErrorIs(t, err, duration.ErrInvalid, "%q", in)
	}
}

func TestFormat(t *testing.T) {
	for minutes, want := range map[int]string{
		0: "0m", 45: "45m", 60: "1h", 150: "2h 30m", 135: "2h 15m", 480: "8h", 1440: "24h", -90: "-1h 30m",
	} {
		require.Equal(t, want, duration.Format(minutes), minutes)
	}
}

// Сценарий приёмки #32: 1h 30m + 45m = 2h 15m / 2.25.
func TestAcceptanceSum(t *testing.T) {
	a, err := duration.Parse("1h 30m")
	require.NoError(t, err)
	b, err := duration.Parse("45m")
	require.NoError(t, err)
	require.Equal(t, "2h 15m", duration.Format(a+b))
	require.Equal(t, "2.25", duration.Hours(a+b))
	require.Equal(t, "2.50", duration.Hours(150))
	require.Equal(t, "0.02", duration.Hours(1))
}
