package routing

import (
	"context"

	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
)

func (h *NativeExecutionHandler) GetCapabilities(ctx context.Context, request *dispatcherprivatev1.GetCapabilitiesRequest) (*dispatcherprivatev1.GetCapabilitiesResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.GetCapabilities(ctx, request)
	}
	return peer.GetCapabilities(forwarded(ctx), request)
}
