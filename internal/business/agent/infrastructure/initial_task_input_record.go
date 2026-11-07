package infrastructure

import (
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// initialTaskInputRecord is the stored shape of a Work's pending initial
// task input, kept unencrypted (Retained configuration storage).
type initialTaskInputRecord struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func encodeInitialTaskInput(input *domain.InitialTaskInput) (string, error) {
	if input == nil {
		return "", nil
	}
	return encode(initialTaskInputRecord{ID: input.ID, Text: input.Text})
}

func decodeInitialTaskInput(data string) (*domain.InitialTaskInput, error) {
	if data == "" {
		return nil, nil
	}
	var record initialTaskInputRecord
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return nil, oops.Wrap(err)
	}
	input, err := domain.NewInitialTaskInput(record.ID, record.Text)
	if err != nil {
		return nil, err
	}
	return &input, nil
}
