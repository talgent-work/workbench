package query

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrInvalidTurnResultQuery = errors.New("agent: Work is required for turn results")

// RootTurnResult is one turn result recorded in a Work's root message stream.
type RootTurnResult struct {
	MessageID  string
	Text       string
	OccurredAt time.Time
}

// RootTurnResultReader reads the root stream's turn results in stream order.
// Results of subagents are not part of the root stream.
type RootTurnResultReader interface {
	ListRootTurnResults(ctx context.Context, workID string) ([]RootTurnResult, error)
}

// WorkTurnResult is a turn result with its position and whether it is the last.
type WorkTurnResult struct {
	Order      int
	Text       string
	Last       bool
	OccurredAt time.Time
	MessageID  string
}

// WorkTurnResults answers the Work Turn Result Query Contract.
type WorkTurnResults struct {
	reader RootTurnResultReader
}

func NewWorkTurnResults(reader RootTurnResultReader) *WorkTurnResults {
	return &WorkTurnResults{reader: reader}
}

// List returns every turn result that reported text, numbered from one, the
// last marked. A turn that ended without text, such as a cancelled one,
// contributes nothing.
func (q *WorkTurnResults) List(ctx context.Context, workID string) ([]WorkTurnResult, error) {
	if workID = strings.TrimSpace(workID); workID == "" {
		return nil, ErrInvalidTurnResultQuery
	}
	recorded, err := q.reader.ListRootTurnResults(ctx, workID)
	if err != nil {
		return nil, err
	}
	results := make([]WorkTurnResult, 0, len(recorded))
	for _, result := range recorded {
		if strings.TrimSpace(result.Text) == "" {
			continue
		}
		results = append(results, WorkTurnResult{Order: len(results) + 1, Text: result.Text, OccurredAt: result.OccurredAt, MessageID: result.MessageID})
	}
	if len(results) > 0 {
		results[len(results)-1].Last = true
	}
	return results, nil
}
