package access_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
)

type owners struct {
	owners []string
	found  bool
	err    error
}

func (o owners) WorkOwners(context.Context, string) ([]string, bool, error) {
	return o.owners, o.found, o.err
}

// Only an Owner of the Work's definition is admitted; an unknown Work and an
// unnamed requester or Work are denied, and a failed read is returned.
func TestAWorkAdmitsOnlyItsDefinitionsOwners(t *testing.T) {
	ctx := context.Background()
	known := access.NewWorkAuthorizer(owners{owners: []string{"owner"}, found: true}.WorkOwners)
	require.NoError(t, known.AuthorizeWork(ctx, "owner", "work"))
	require.ErrorIs(t, known.AuthorizeWork(ctx, "someone", "work"), access.ErrAccessDenied)
	require.ErrorIs(t, known.AuthorizeWork(ctx, " ", "work"), access.ErrAccessDenied)
	require.ErrorIs(t, known.AuthorizeWork(ctx, "owner", ""), access.ErrAccessDenied)
	require.ErrorIs(t, access.NewWorkAuthorizer(owners{}.WorkOwners).AuthorizeWork(ctx, "owner", "work"), access.ErrAccessDenied)
	failure := errors.New("database unavailable")
	require.ErrorIs(t, access.NewWorkAuthorizer(owners{err: failure}.WorkOwners).AuthorizeWork(ctx, "owner", "work"), failure)
}
