package routing

import (
	"context"

	"connectrpc.com/connect"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
)

func (h *NativeExecutionHandler) GetCapabilities(ctx context.Context, request *connect.Request[dispatcherprivatev1.GetCapabilitiesRequest]) (*connect.Response[dispatcherprivatev1.GetCapabilitiesResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.GetCapabilities(ctx, request)
	}
	return peer.GetCapabilities(ctx, forwardNativeRequest(request))
}
