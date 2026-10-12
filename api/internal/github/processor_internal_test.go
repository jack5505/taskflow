package github

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Обработчик github.delivery разбирает доставку; битый payload — ошибка (событие повторится).
func TestProcessorHandle(t *testing.T) {
	p := NewProcessor()
	ok, err := json.Marshal(Delivery{Event: "ping", Delivery: "d-1", Payload: json.RawMessage(`{}`)})
	require.NoError(t, err)
	require.NoError(t, p.handle(context.Background(), events.Envelope{Type: events.GitHubDelivery, Payload: ok}))
	require.Error(t, p.handle(context.Background(), events.Envelope{Type: events.GitHubDelivery, Payload: []byte("{")}))
	d := events.NewDispatcher(nil)
	Register(d, p)
}
