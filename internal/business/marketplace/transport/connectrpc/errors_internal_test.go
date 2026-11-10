package connectrpc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
)

// A caller that cancels or times out is told so, not that Marketplace failed.
func TestMapErrorKeepsTheCallersCancellation(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code connect.Code
	}{
		"canceled":          {err: fmt.Errorf("resolve plugin: %w", context.Canceled), code: connect.CodeCanceled},
		"deadline exceeded": {err: fmt.Errorf("resolve plugin: %w", context.DeadlineExceeded), code: connect.CodeDeadlineExceeded},
		"anything else":     {err: errors.New("provider exploded"), code: connect.CodeInternal},
	} {
		t.Run(name, func(t *testing.T) {
			if got := connect.CodeOf(mapError(tc.err)); got != tc.code {
				t.Fatalf("code = %v, want %v", got, tc.code)
			}
		})
	}
}
