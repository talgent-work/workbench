package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// An execution keeps its LaunchSpecification, its Plugin preparation bound
// included, through the storage round trip.
func TestStoredLaunchKeepsThePreparationBound(t *testing.T) {
	request := request()
	request.ThinkingLevel = "high"
	request.PreparationBound = time.Now().Add(13 * time.Minute).UTC().Truncate(time.Millisecond)
	stored, err := encodeLaunch(request)
	require.NoError(t, err)

	decoded, err := decodeStartRequest(stored)
	require.NoError(t, err)
	require.Equal(t, request.WorkID, decoded.WorkID)
	require.Equal(t, request.ThinkingLevel, decoded.ThinkingLevel)
	require.True(t, request.PreparationBound.Equal(decoded.PreparationBound))
}

// A sandboxed start survives the storage round trip and is stored apart from
// an unsandboxed one.
func TestStoredLaunchOfASandboxedStart(t *testing.T) {
	request := request()
	current, err := encodeLaunch(request)
	require.NoError(t, err)

	request.Capabilities.Sandbox = true
	sandboxed, err := encodeLaunch(request)
	require.NoError(t, err)
	require.NotEqual(t, string(current), string(sandboxed))
	decoded, err := decodeStartRequest(sandboxed)
	require.NoError(t, err)
	require.True(t, decoded.Capabilities.Sandbox)
}

// A start on the machine's own login is stored apart from one on a Model
// Configuration and survives the storage round trip.
func TestStoredLaunchOfAStartOnTheMachineLogin(t *testing.T) {
	request := request()
	current, err := encodeLaunch(request)
	require.NoError(t, err)

	request.WorkModel = domain.MachineLoginModel()
	machine, err := encodeLaunch(request)
	require.NoError(t, err)
	require.NotEqual(t, string(current), string(machine))
	decoded, err := decodeStartRequest(machine)
	require.NoError(t, err)
	require.Equal(t, domain.MachineLoginModel(), decoded.WorkModel)
}
