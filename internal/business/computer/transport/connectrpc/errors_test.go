package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

func TestPublicErrorKeepsTheCauseKind(t *testing.T) {
	require.Equal(t, connect.CodeDeadlineExceeded, connect.CodeOf(publicError(context.DeadlineExceeded)))
	require.Equal(t, connect.CodeCanceled, connect.CodeOf(publicError(context.Canceled)))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(publicError(domain.ErrComputerNotFound)))
	require.Equal(t, connect.CodeAborted, connect.CodeOf(publicError(domain.ErrConcurrentModification)))
}
