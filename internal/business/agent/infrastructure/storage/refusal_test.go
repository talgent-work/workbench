package storage

import (
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
)

// A refusal received from Storage names its reason in an ErrorInfo of
// Storage's domain; another domain's detail or a plain error names none.
func TestRefusalReasonReadsStoragesErrorInfo(t *testing.T) {
	refused := func(domain string) error {
		detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Domain: domain, Reason: storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_RECLAIMED.String()})
		require.NoError(t, err)
		return connect.NewError(connect.CodeFailedPrecondition, "reclaimed").WithDetail(detail).WithRemote()
	}
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_RECLAIMED, refusalReason(refused("storage")))
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED, refusalReason(refused("account")))
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED, refusalReason(connect.NewError(connect.CodeNotFound, "plain")))
}
