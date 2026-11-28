package infrastructure

import (
	"encoding/json"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// encodeVariableSelection writes a Work Variable Selection as the JSON array
// of its names, an empty array for none.
func encodeVariableSelection(selection domain.WorkVariableSelection) (string, error) {
	names := selection.Names
	if names == nil {
		names = []string{}
	}
	return encode(names)
}

// decodeVariableSelection reads a stored Work Variable Selection, refusing
// one that is not a valid selection.
func decodeVariableSelection(data string) (domain.WorkVariableSelection, error) {
	var selection domain.WorkVariableSelection
	if err := json.Unmarshal([]byte(data), &selection.Names); err != nil {
		return domain.WorkVariableSelection{}, oops.With("operation", "decode_work_variable_selection").Wrap(err)
	}
	if len(selection.Names) == 0 {
		selection.Names = nil
	}
	return selection, selection.Validate()
}
