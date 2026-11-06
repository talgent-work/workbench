package connectrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
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
	handler := NewTurnResultHandler(query.NewWorkTurnResults(turnReader{results: []query.RootTurnResult{{MessageID: "a", Text: "one", OccurredAt: at}, {MessageID: "b", Text: "two", OccurredAt: at}}}), "control")
	_, err := handler.ListWorkTurnResults(context.Background(), connect.NewRequest(&agentprivatev1.ListWorkTurnResultsRequest{WorkId: "w"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	request := connect.NewRequest(&agentprivatev1.ListWorkTurnResultsRequest{WorkId: "w"})
	request.Header().Set("Authorization", "Bearer control")
	response, err := handler.ListWorkTurnResults(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, response.Msg.GetResults(), 2)
	require.False(t, response.Msg.GetResults()[0].GetLast())
	require.True(t, response.Msg.GetResults()[1].GetLast())
	require.EqualValues(t, 2, response.Msg.GetResults()[1].GetOrder())

	failing := NewTurnResultHandler(query.NewWorkTurnResults(turnReader{err: errors.New("clickhouse down")}), "control")
	_, err = failing.ListWorkTurnResults(context.Background(), request)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}
