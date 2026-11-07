package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
)

// listedInteractions lists the Harness's pending interactions over a test's
// input buffer.
type listedInteractions struct {
	*commandtest.InputBuffer
	pending []command.PendingInteraction
	err     error
}

func (l listedInteractions) ListPendingInteractions(context.Context, string) ([]command.PendingInteraction, error) {
	return l.pending, l.err
}

// A required interaction needs a response until the Work's buffer holds a
// reply to it.
func TestRequiredResponseMatchesPendingInteractionsWithBufferedReplies(t *testing.T) {
	ctx := context.Background()
	answered := &commandtest.InputBuffer{Replies: []command.InteractionResponse{{InteractionID: "a"}}}
	for name, c := range map[string]struct {
		pending  []command.PendingInteraction
		required bool
	}{
		"none pending":   {},
		"all answered":   {pending: []command.PendingInteraction{{ID: "a"}}},
		"one unanswered": {pending: []command.PendingInteraction{{ID: "a"}, {ID: "b"}}, required: true},
	} {
		t.Run(name, func(t *testing.T) {
			required, err := application.NewRequiredResponseReader(listedInteractions{InputBuffer: answered, pending: c.pending})(ctx, "work")
			require.NoError(t, err)
			require.Equal(t, c.required, required)
		})
	}
	unavailable := errors.New("dispatcher unavailable")
	_, err := application.NewRequiredResponseReader(listedInteractions{InputBuffer: answered, err: unavailable})(ctx, "work")
	require.ErrorIs(t, err, unavailable)
	_, err = application.NewRequiredResponseReader(listedInteractions{InputBuffer: &commandtest.InputBuffer{Err: unavailable}, pending: []command.PendingInteraction{{ID: "b"}}})(ctx, "work")
	require.ErrorIs(t, err, unavailable)
}
