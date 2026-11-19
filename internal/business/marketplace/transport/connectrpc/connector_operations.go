package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// ConnectorOperationHandler serves the platform operator's Connector
// commands, admitted by the private credential token.
type ConnectorOperationHandler struct {
	app            *application.Application
	authentication *CredentialHandler
}

var _ marketplaceprivatev1connect.ConnectorOperationServiceHandler = (*ConnectorOperationHandler)(nil)

func NewConnectorOperationHandler(app *application.Application, token string) *ConnectorOperationHandler {
	return &ConnectorOperationHandler{app, NewCredentialHandler(nil, token)}
}

func (h *ConnectorOperationHandler) WithdrawConnector(ctx context.Context, request *marketplaceprivatev1.WithdrawConnectorRequest) (*marketplaceprivatev1.WithdrawConnectorResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authentication.authenticate(info.RequestHeader()); err != nil {
		return nil, err
	}
	name, err := h.app.WithdrawConnectorAsOperator(ctx, request.GetConnectorId())
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplaceprivatev1.WithdrawConnectorResponse{Name: name}, nil
}
