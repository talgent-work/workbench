package connectrpc

import (
	"context"

	"connectrpc.com/connect"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
)

func (h *ExecutionHandler) GetCapabilities(ctx context.Context, request *connect.Request[dispatcherprivatev1.GetCapabilitiesRequest]) (*connect.Response[dispatcherprivatev1.GetCapabilitiesResponse], error) {
	if err := h.authenticate(request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	value, err := h.app.GetCapabilities(ctx, request.Msg.GetWorkId())
	if err != nil {
		return nil, mapNativeExecutionError(err)
	}
	result := &dispatcherprivatev1.GetCapabilitiesResponse{Observed: value.Observed}
	for _, item := range value.Commands {
		result.Commands = append(result.Commands, &dispatcherprivatev1.NativeCommand{Name: item.Name, Description: item.Description, ArgumentHint: item.ArgumentHint})
	}
	return connect.NewResponse(result), nil
}
