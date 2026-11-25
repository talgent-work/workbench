package dispatcher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Fencing ends a reading whose reader holds its lock, as a reader waiting on
// an unreachable Redis does when liveness lapses.
func TestFencingDoesNotWaitForABusyReader(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	entry := &nativeExecution{ctx: ctx, cancel: cancel}
	m := &NativeExecutions{connections: map[string]*nativeExecution{"work-1": entry}}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	m.fenceAll()

	require.ErrorIs(t, context.Cause(entry.ctx), errFenced)
}
