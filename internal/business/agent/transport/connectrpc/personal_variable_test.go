package connectrpc

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/samber/oops"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A refused Personal Variable answers the broken rule as its person reads it,
// with no internal prefix; a failure the person cannot act on answers a fixed
// message, whatever its cause says.
func TestPersonalVariableRefusalsAnswerTheRuleTheyBreak(t *testing.T) {
	for _, refusal := range []error{
		domain.ErrInvalidPersonalVariableName,
		domain.ErrReservedPersonalVariableName,
		domain.ErrInvalidPersonalVariableValue,
		command.ErrTooManyPersonalVariables,
		command.ErrPersonalVariableTooLarge,
		command.ErrPersonalVariablesTooLarge,
	} {
		mapped := new(connect.Error)
		require.True(t, errors.As(mapPersonalVariableError(fmt.Errorf("set: %w", refusal)), &mapped))
		require.Equal(t, connect.CodeInvalidArgument, mapped.Code())
		require.NotContains(t, mapped.Message(), "agent:")
		require.Equal(t, refusal.(*domain.PersonalVariableRefusal).Reason, mapped.Message())
	}
	require.Equal(t, "Names starting with TALGENT_, in any letter case, are reserved for the platform.",
		mapPersonalVariableError(domain.ErrReservedPersonalVariableName).(*connect.Error).Message())

	aborted := mapPersonalVariableError(domain.ErrConcurrentModification).(*connect.Error)
	require.Equal(t, connect.CodeAborted, aborted.Code(), "a set that kept changing may be retried")

	corrupt := mapPersonalVariableError(oops.Errorf("a stored Personal Variable set breaks its rules: %s", domain.ErrInvalidPersonalVariableName)).(*connect.Error)
	require.Equal(t, connect.CodeInternal, corrupt.Code())
	require.Equal(t, "agent: Personal Variables are unavailable", corrupt.Message())
}
