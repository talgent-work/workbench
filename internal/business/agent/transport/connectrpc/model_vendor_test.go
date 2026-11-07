package connectrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// Every vendor a Model Template may name reaches the published contract.
func TestEveryModelVendorHasAPublishedValue(t *testing.T) {
	seen := map[agentpublicv1.ModelVendor]bool{}
	for _, vendor := range query.ModelVendors {
		value, ok := modelVendors[vendor]
		require.True(t, ok, vendor)
		require.NotEqual(t, agentpublicv1.ModelVendor_MODEL_VENDOR_UNSPECIFIED, value, vendor)
		require.False(t, seen[value], "%s shares a published value", vendor)
		seen[value] = true
	}
	require.Len(t, modelVendors, len(query.ModelVendors), "the published mapping names no unknown vendor")
}
