package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/dispatcher/private/v1"
)

func (h *ExecutionHandler) GetCapabilities(ctx context.Context, request *dispatcherprivatev1.GetCapabilitiesRequest) (*dispatcherprivatev1.GetCapabilitiesResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	value, err := h.app.GetCapabilities(ctx, request.GetWorkId())
	if err != nil {
		return nil, mapNativeExecutionError(err)
	}
	result := &dispatcherprivatev1.GetCapabilitiesResponse{Observed: value.Observed}
	for _, item := range value.Commands {
		result.Commands = append(result.Commands, &dispatcherprivatev1.NativeCommand{Name: item.Name, Description: item.Description, ArgumentHint: item.ArgumentHint})
	}
	return result, nil
}
