package connectrpc

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// Only the caller's own invalid input is InvalidArgument; a stored
// Application that cannot be read back is the server's fault and names none
// of its internals.
func TestApplicationErrorsKeepServerFaultsInternal(t *testing.T) {
	invalidName := fmt.Errorf("%w: a name has 1 to %d characters", domain.ErrInvalidApplication, domain.MaxApplicationName)
	err := applicationError(invalidName)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	storedFault := errors.New("stored application is not valid: invalid application: unknown client secret state 7")
	err = applicationError(storedFault)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	var connectErr *connect.Error
	require.True(t, errors.As(err, &connectErr))
	require.NotContains(t, connectErr.Message(), "state")
}
