package infrastructure

import (
	"encoding/json"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// encodeVariableSelection writes a Work Variable Selection as the JSON array
// of its names, an empty array for none.
func encodeVariableSelection(selection domain.WorkVariableSelection) ([]byte, error) {
	names := selection.Names
	if names == nil {
		names = []string{}
	}
	body, err := json.Marshal(names)
	if err != nil {
		return nil, oops.Wrap(err)
	}
	return body, nil
}

// decodeVariableSelection reads a stored Work Variable Selection; a Work
// stored before it existed (NULL) enabled none.
func decodeVariableSelection(data []byte) (domain.WorkVariableSelection, error) {
	var selection domain.WorkVariableSelection
	if len(data) > 0 {
		if err := json.Unmarshal(data, &selection.Names); err != nil {
			return domain.WorkVariableSelection{}, oops.With("operation", "decode_work_variable_selection").Wrap(err)
		}
	}
	return selection, selection.Validate()
}
