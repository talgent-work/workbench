package execution

import (
	"encoding/json"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// checkoutResultRecord is one repository's checkout outcome in a run.
type checkoutResultRecord struct {
	RepositoryID string `json:"repository_id"`
	CheckedOut   bool   `json:"checked_out"`
	Reason       string `json:"reason,omitempty"`
}

func encodeCheckoutResults(results []domain.RepositoryCheckout) ([]byte, error) {
	if len(results) == 0 {
		return []byte("null"), nil
	}
	records := make([]checkoutResultRecord, 0, len(results))
	for _, result := range results {
		records = append(records, checkoutResultRecord{RepositoryID: result.RepositoryID, CheckedOut: result.CheckedOut, Reason: result.Reason})
	}
	return json.Marshal(records)
}

func decodeCheckoutResults(data []byte) ([]domain.RepositoryCheckout, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var records []checkoutResultRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	var results []domain.RepositoryCheckout
	for _, record := range records {
		results = append(results, domain.RepositoryCheckout{RepositoryID: record.RepositoryID, CheckedOut: record.CheckedOut, Reason: record.Reason})
	}
	return results, nil
}
