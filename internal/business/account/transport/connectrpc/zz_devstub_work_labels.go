package connectrpc

// Development stub while the Work Labels branch builds against a contracts
// release that already declares RemoveApplication; deleted when the branch
// that implements it is merged.

import (
	"context"

	"connectrpc.com/connect/v2"
	accountpublicv1 "github.com/talgent-work/contracts/gen/go/account/public/v1"
)

func (*Applications) RemoveApplication(context.Context, *accountpublicv1.RemoveApplicationRequest) (*accountpublicv1.RemoveApplicationResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, "not implemented on this branch")
}
