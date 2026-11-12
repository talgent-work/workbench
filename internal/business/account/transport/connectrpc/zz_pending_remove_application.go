package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"
	accountpublicv1 "github.com/talgent-work/contracts/gen/go/account/public/v1"
)

// PENDING-MERGE: RemoveApplication lands with Application removal; this file
// goes when that change merges.
func (h *Applications) RemoveApplication(context.Context, *accountpublicv1.RemoveApplicationRequest) (*accountpublicv1.RemoveApplicationResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, "account: removing an application is not available yet")
}
