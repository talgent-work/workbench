package infrastructure

import (
	"encoding/json"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// startingContextRecord is the persisted shape of a Work's starting context;
// an empty column means the Work started afresh.
type startingContextRecord struct {
	SourceWorkID           string `json:"source_work_id"`
	SourceSessionReference string `json:"source_session_reference"`
	Position               string `json:"position"`
}

func encodeStartingContext(starting *domain.StartingContext) (string, error) {
	if starting == nil {
		return "", nil
	}
	return encode(startingContextRecord{SourceWorkID: starting.SourceWorkID, SourceSessionReference: starting.SourceSessionReference, Position: starting.Position})
}

func decodeStartingContext(data string) (*domain.StartingContext, error) {
	if data == "" {
		return nil, nil
	}
	var record startingContextRecord
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return nil, oops.Wrap(err)
	}
	return &domain.StartingContext{SourceWorkID: record.SourceWorkID, SourceSessionReference: record.SourceSessionReference, Position: record.Position}, nil
}
