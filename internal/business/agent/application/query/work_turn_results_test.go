package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

type rootResults []query.RootTurnResult

func (r rootResults) ListRootTurnResults(context.Context, string) ([]query.RootTurnResult, error) {
	return r, nil
}

// Turn results keep stream order; the last one reporting text is the final
// conclusion, and a turn without text takes no position.
func TestWorkTurnResultsNumberAndMarkTheLast(t *testing.T) {
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	results, err := query.NewWorkTurnResults(rootResults{
		{MessageID: "m1", Text: "Explored two options", OccurredAt: at},
		{MessageID: "m2", Text: "  ", OccurredAt: at.Add(time.Minute)},
		{MessageID: "m3", Text: "Done: option B", OccurredAt: at.Add(2 * time.Minute)},
	}).List(context.Background(), "work")
	require.NoError(t, err)
	require.Equal(t, []query.WorkTurnResult{
		{Order: 1, Text: "Explored two options", OccurredAt: at, MessageID: "m1"},
		{Order: 2, Text: "Done: option B", Last: true, OccurredAt: at.Add(2 * time.Minute), MessageID: "m3"},
	}, results)
}

func TestWorkTurnResultsWithoutResults(t *testing.T) {
	results, err := query.NewWorkTurnResults(rootResults{}).List(context.Background(), "work")
	require.NoError(t, err)
	require.Empty(t, results)
	_, err = query.NewWorkTurnResults(rootResults{}).List(context.Background(), " ")
	require.ErrorIs(t, err, query.ErrInvalidTurnResultQuery)
}
