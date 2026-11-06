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

type listedInteractions struct {
	pending []application.PendingInteraction
	err     error
}

func (l listedInteractions) ListPendingInteractions(context.Context, string) ([]application.PendingInteraction, error) {
	return l.pending, l.err
}

// A required interaction needs a response until the Work's buffer holds a
// reply to it.
func TestRequiredResponseMatchesPendingInteractionsWithBufferedReplies(t *testing.T) {
	ctx := context.Background()
	answered := &commandtest.InputBuffer{Replies: []command.InteractionResponse{{InteractionID: "a"}}}
	for name, c := range map[string]struct {
		pending  []application.PendingInteraction
		required bool
	}{
		"none pending":   {},
		"all answered":   {pending: []application.PendingInteraction{{ID: "a"}}},
		"one unanswered": {pending: []application.PendingInteraction{{ID: "a"}, {ID: "b"}}, required: true},
	} {
		t.Run(name, func(t *testing.T) {
			required, err := application.NewRequiredResponseReader(listedInteractions{pending: c.pending}, answered)(ctx, "work")
			require.NoError(t, err)
			require.Equal(t, c.required, required)
		})
	}
	unavailable := errors.New("dispatcher unavailable")
	_, err := application.NewRequiredResponseReader(listedInteractions{err: unavailable}, answered)(ctx, "work")
	require.ErrorIs(t, err, unavailable)
	_, err = application.NewRequiredResponseReader(listedInteractions{pending: []application.PendingInteraction{{ID: "b"}}}, &commandtest.InputBuffer{Err: unavailable})(ctx, "work")
	require.ErrorIs(t, err, unavailable)
}
