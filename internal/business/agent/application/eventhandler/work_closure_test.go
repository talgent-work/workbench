package eventhandler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Work whose execution ended, or that entered Closing, has its recovery
// woken: recovery applies the kept close and empties the input buffer.
func TestWorkClosuresWakeTheWorksRecovery(t *testing.T) {
	handler := eventhandler.NewWorkClosures()
	require.ElementsMatch(t, []domain.EventKind{domain.EventKindExecutionEnded, domain.EventKindClosing}, handler.Listening())
	handler.Handle(context.Background(), domain.WorkExecutionEnded{WorkID: "unwired"}) // the sweep carries it out
	var woken []string
	handler.Wake = func(_ context.Context, id string) { woken = append(woken, id) }
	handler.Handle(context.Background(), domain.WorkExecutionEnded{WorkID: "stopped"})
	handler.Handle(context.Background(), domain.WorkClosingEntered{WorkID: "closing"})
	handler.Handle(context.Background(), domain.WorkMadePrivate{WorkID: "other"})
	require.Equal(t, []string{"stopped", "closing"}, woken)
}
