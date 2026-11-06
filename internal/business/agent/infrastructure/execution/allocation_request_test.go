package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A start's Plugin preparation bound never changes its identity.
func TestAllocationIdentityIgnoresThePreparationBound(t *testing.T) {
	request := request()
	request.ThinkingLevel = "high"
	unbounded, err := canonicalRequest(request)
	require.NoError(t, err)

	request.PreparationBound = time.Now().Add(13 * time.Minute)
	current, err := canonicalRequest(request)
	require.NoError(t, err)
	require.Equal(t, string(unbounded), string(current))

	decoded, err := decodeStartRequest(current)
	require.NoError(t, err)
	require.Equal(t, request.WorkID, decoded.WorkID)
	require.Equal(t, request.ThinkingLevel, decoded.ThinkingLevel)
	require.True(t, decoded.PreparationBound.IsZero(), "the bound is used only while registering")
}

// A sandboxed start is a different start and survives the storage round trip.
func TestAllocationIdentityOfASandboxedStart(t *testing.T) {
	request := request()
	current, err := canonicalRequest(request)
	require.NoError(t, err)

	request.Capabilities.Sandbox = true
	sandboxed, err := canonicalRequest(request)
	require.NoError(t, err)
	require.NotEqual(t, string(current), string(sandboxed))
	decoded, err := decodeStartRequest(sandboxed)
	require.NoError(t, err)
	require.True(t, decoded.Capabilities.Sandbox)
}

// A start on the machine's own login is a different start than one on a
// Model Configuration and survives the storage round trip.
func TestAllocationIdentityOfAStartOnTheMachineLogin(t *testing.T) {
	request := request()
	current, err := canonicalRequest(request)
	require.NoError(t, err)

	request.WorkModel = domain.MachineLoginModel()
	machine, err := canonicalRequest(request)
	require.NoError(t, err)
	require.NotEqual(t, string(current), string(machine))
	decoded, err := decodeStartRequest(machine)
	require.NoError(t, err)
	require.Equal(t, domain.MachineLoginModel(), decoded.WorkModel)
}
