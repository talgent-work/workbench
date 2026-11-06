package domain_test

import (
	"testing"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// One Work's events are handled in order; different Works' run concurrently.
func TestEventsAreKeyedByTheirWork(t *testing.T) {
	events := []domain.Event{
		domain.WorkExecutionEnded{WorkID: "work"},
		domain.WorkClosingEntered{WorkID: "work"},
		domain.WorkClosedEvent{WorkID: "work"},
		domain.WorkShareRevoked{WorkID: "work", ReaderID: "reader"},
		domain.WorkMadePrivate{WorkID: "work"},
	}
	for _, event := range events {
		keyed, ok := event.(domainevent.Keyed)
		require.True(t, ok, event.Kind())
		require.Equal(t, "work", keyed.Key(), event.Kind())
	}
}
