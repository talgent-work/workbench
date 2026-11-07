package connectrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/contracts/gen/agent/private/v1"
	"github.com/talgent-work/contracts/gen/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

type turnReader struct {
	results []query.RootTurnResult
	err     error
}

func (r turnReader) ListRootTurnResults(context.Context, string) ([]query.RootTurnResult, error) {
	return r.results, r.err
}

func TestTurnResultsRequireControlTokenAndMarkTheLast(t *testing.T) {
	at := time.UnixMilli(1000).UTC()
	handler := turnResultsClient(NewTurnResultHandler(query.NewWorkTurnResults(turnReader{results: []query.RootTurnResult{{MessageID: "a", Text: "one", OccurredAt: at}, {MessageID: "b", Text: "two", OccurredAt: at}}}), "control"))
	_, err := handler.ListWorkTurnResults(context.Background(), &agentprivatev1.ListWorkTurnResultsRequest{WorkId: "w"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	request := &agentprivatev1.ListWorkTurnResultsRequest{WorkId: "w"}
	authorized, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set("Authorization", "Bearer control")
	response, err := handler.ListWorkTurnResults(authorized, request)
	require.NoError(t, err)
	require.Len(t, response.GetResults(), 2)
	require.False(t, response.GetResults()[0].GetLast())
	require.True(t, response.GetResults()[1].GetLast())
	require.EqualValues(t, 2, response.GetResults()[1].GetOrder())

	failing := turnResultsClient(NewTurnResultHandler(query.NewWorkTurnResults(turnReader{err: errors.New("clickhouse down")}), "control"))
	_, err = failing.ListWorkTurnResults(authorized, request)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

// turnResultsClient calls handler through Connect, in process.
func turnResultsClient(handler agentprivatev1connect.WorkTurnResultServiceHandler) agentprivatev1connect.WorkTurnResultServiceClient {
	server := connect.NewServer()
	agentprivatev1connect.RegisterWorkTurnResultServiceHandler(server, handler)
	return agentprivatev1connect.NewWorkTurnResultServiceClient(connect.NewClient(connectinprocess.New(server)))
}
