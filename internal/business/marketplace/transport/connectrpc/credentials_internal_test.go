package connectrpc

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// A refusal tells the consumer whether it confirms anything about the
// authorization: a deleted one is as unusable as a revoked one, while an
// unexplained failure stays "unavailable" for the consumer to retry.
func TestCredentialRefusalsNameConfirmedReasons(t *testing.T) {
	for err, reason := range map[error]string{
		domain.ErrAuthorizationRevoked:           "revoked",
		domain.ErrAuthorizationReconnectRequired: "reconnect_required",
		domain.ErrProviderNotFound:               "revoked",
		domain.ErrPermissionDenied:               "access_denied",
		errors.New("github timed out"):           "unavailable",
	} {
		var mapped *connect.Error
		require.ErrorAs(t, credentialSupplyError(err), &mapped)
		require.Equal(t, reason, mapped.Meta().Get("X-Talgent-Credential-Status"), err.Error())
	}
}
