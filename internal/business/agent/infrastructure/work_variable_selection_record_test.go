package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestStoredWorkVariableSelectionRoundTripsItsNames(t *testing.T) {
	for _, selection := range []domain.WorkVariableSelection{{}, {Names: []string{"A_TOKEN", "B_TOKEN"}}} {
		encoded, err := encodeVariableSelection(selection)
		require.NoError(t, err)
		decoded, err := decodeVariableSelection(encoded)
		require.NoError(t, err)
		require.Equal(t, selection, decoded)
	}
	encoded, err := encodeVariableSelection(domain.WorkVariableSelection{})
	require.NoError(t, err)
	require.Equal(t, "[]", encoded, "none is stored as an empty array")
}

// A stored selection that is not JSON, or that breaks the selection's own
// rules, fails the read instead of enabling variables the Work never chose.
func TestStoredWorkVariableSelectionRefusesAnInvalidRecord(t *testing.T) {
	_, err := decodeVariableSelection(`{"names":`)
	require.Error(t, err)
	for _, record := range []string{`["B_TOKEN","A_TOKEN"]`, `["A_TOKEN","A_TOKEN"]`, `["not a name"]`} {
		_, err := decodeVariableSelection(record)
		require.ErrorIs(t, err, domain.ErrInvalidWork, record)
	}
}
