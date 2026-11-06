package connectrpc

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A start refused for a Computer without the sandbox, or below the Minimum
// Version, carries the detail Start Work tells it apart by; another Computer
// refusal does not.
func TestASandboxRefusalCarriesItsDetail(t *testing.T) {
	var refused *connect.Error
	require.ErrorAs(t, mapAgentError(domain.ErrComputerNoSandbox), &refused)
	require.Equal(t, connect.CodeFailedPrecondition, refused.Code())
	require.Len(t, refused.Details(), 1)
	value, err := refused.Details()[0].Value()
	require.NoError(t, err)
	require.IsType(t, &agentpublicv1.SandboxUnavailable{}, value)

	require.ErrorAs(t, mapAgentError(domain.ErrComputerOutdated), &refused)
	require.Equal(t, connect.CodeFailedPrecondition, refused.Code())
	require.Len(t, refused.Details(), 1)
	value, err = refused.Details()[0].Value()
	require.NoError(t, err)
	require.IsType(t, &agentpublicv1.ComputerOutdated{}, value)

	require.ErrorAs(t, mapAgentError(domain.ErrComputerUnreachable), &refused)
	require.Empty(t, refused.Details())
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(mapAgentError(domain.ErrPermissionModeStarterOnly)))
}

// Every mode the Harness reports reaches the browser by name.
func TestEveryHarnessPermissionModeHasAPublicName(t *testing.T) {
	for _, mode := range domain.HarnessPermissionModes {
		require.NotEqual(t, agentpublicv1.PermissionMode_PERMISSION_MODE_UNSPECIFIED, permissionModeToProto(mode), string(mode))
	}
}

// An unobserved execution environment is an expected, retryable condition.
func TestAnUnobservedEnvironmentIsUnavailable(t *testing.T) {
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(mapAgentError(errors.Join(domain.ErrExecutionUnobserved, domain.ErrExecutionRequest))))
}
