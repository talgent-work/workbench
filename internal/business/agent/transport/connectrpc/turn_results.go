package connectrpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// TurnResultHandler serves a Work's turn results to internal consumers that
// hold the Agent control token.
type TurnResultHandler struct {
	results *query.WorkTurnResults
	token   string
}

var _ agentprivatev1connect.WorkTurnResultServiceHandler = (*TurnResultHandler)(nil)

func NewTurnResultHandler(results *query.WorkTurnResults, token string) *TurnResultHandler {
	return &TurnResultHandler{results: results, token: token}
}

func (h *TurnResultHandler) ListWorkTurnResults(ctx context.Context, request *connect.Request[agentprivatev1.ListWorkTurnResultsRequest]) (*connect.Response[agentprivatev1.ListWorkTurnResultsResponse], error) {
	if strings.TrimSpace(h.token) == "" || subtle.ConstantTimeCompare([]byte(request.Header().Get("Authorization")), []byte("Bearer "+h.token)) != 1 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("agent: control authentication required"))
	}
	results, err := h.results.List(ctx, request.Msg.GetWorkId())
	if errors.Is(err, query.ErrInvalidTurnResultQuery) {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("agent: turn results unavailable"))
	}
	response := &agentprivatev1.ListWorkTurnResultsResponse{Results: make([]*agentprivatev1.WorkTurnResult, 0, len(results))}
	for _, result := range results {
		response.Results = append(response.Results, &agentprivatev1.WorkTurnResult{
			Order: int32(result.Order), Text: result.Text, Last: result.Last, OccurredAt: timestamppb.New(result.OccurredAt), MessageId: result.MessageID,
		})
	}
	return connect.NewResponse(response), nil
}
