package execution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// previousAllocationRequest is the start request as releases stored and
// fingerprinted it before a start carried a Plugin preparation bound.
type previousAllocationRequest struct {
	RepositorySelection     domain.WorkRepositorySelection
	AuthorizationSelection  domain.WorkAuthorizationSelection
	ThinkingLevel           string `json:",omitempty"`
	WorkModel               previousWorkModel
	AgentID                 string
	WorkID                  string
	Capabilities            previousCapabilities
	HarnessSessionReference string
	ContextMode             domain.ContextMode
	WorkspaceReference      string
	HomeBinding             domain.HomeBinding
	TraceAttributes         map[string]string
}

// previousWorkModel is the model choice as earlier releases stored and
// fingerprinted it, before a Work could choose the machine's own login.
type previousWorkModel struct {
	ConfigurationID string
}

// previousCapabilities is the capability snapshot as earlier releases
// stored and fingerprinted it, before a Work could ask for the sandbox.
type previousCapabilities struct {
	Prompt       string
	StartupAgent string
	Plugins      []domain.SelectedPlugin
	Connectors   []domain.SelectedConnector
}

// A live allocation registered before the release still matches the same
// start, and a start's Plugin preparation bound never changes its identity.
func TestAllocationIdentityIgnoresThePreparationBound(t *testing.T) {
	request := request()
	request.ThinkingLevel = "high"
	previous, err := json.Marshal(previousAllocationRequest{RepositorySelection: request.RepositorySelection, AuthorizationSelection: request.AuthorizationSelection, ThinkingLevel: request.ThinkingLevel, WorkModel: previousWorkModel{ConfigurationID: request.WorkModel.ConfigurationID}, AgentID: request.AgentID, WorkID: request.WorkID,
		Capabilities: previousCapabilities{Prompt: request.Capabilities.Prompt, StartupAgent: request.Capabilities.StartupAgent, Plugins: request.Capabilities.Plugins, Connectors: request.Capabilities.Connectors}, HarnessSessionReference: request.HarnessSessionReference, ContextMode: request.ContextMode, WorkspaceReference: request.WorkspaceReference, HomeBinding: request.HomeBinding, TraceAttributes: request.TraceAttributes})
	require.NoError(t, err)

	request.PreparationBound = time.Now().Add(13 * time.Minute)
	current, err := canonicalRequest(request)
	require.NoError(t, err)
	require.Equal(t, string(previous), string(current))

	decoded, err := decodeStartRequest(current)
	require.NoError(t, err)
	require.Equal(t, request.WorkID, decoded.WorkID)
	require.Equal(t, request.ThinkingLevel, decoded.ThinkingLevel)
	require.True(t, decoded.PreparationBound.IsZero(), "the bound is used only while registering")
}

// A start that does not ask for the sandbox keeps the fingerprint earlier
// releases gave it, so their live allocations still match; a sandboxed
// start is a different start and survives the storage round trip.
func TestAllocationIdentityOfAStartWithoutTheSandboxIsUnchanged(t *testing.T) {
	request := request()
	current, err := canonicalRequest(request)
	require.NoError(t, err)
	require.NotContains(t, string(current), "Sandbox")
	require.Contains(t, string(current), `"Capabilities":{"Prompt":`)

	request.Capabilities.Sandbox = true
	sandboxed, err := canonicalRequest(request)
	require.NoError(t, err)
	require.NotEqual(t, string(current), string(sandboxed))
	decoded, err := decodeStartRequest(sandboxed)
	require.NoError(t, err)
	require.True(t, decoded.Capabilities.Sandbox)
}

// A start on a Model Configuration keeps the fingerprint earlier releases
// gave it; a start on the machine's own login is a different start and
// survives the storage round trip.
func TestAllocationIdentityOfAStartOnAModelConfigurationIsUnchanged(t *testing.T) {
	request := request()
	current, err := canonicalRequest(request)
	require.NoError(t, err)
	require.NotContains(t, string(current), "MachineLogin")

	request.WorkModel = domain.MachineLoginModel()
	machine, err := canonicalRequest(request)
	require.NoError(t, err)
	require.NotEqual(t, string(current), string(machine))
	decoded, err := decodeStartRequest(machine)
	require.NoError(t, err)
	require.Equal(t, domain.MachineLoginModel(), decoded.WorkModel)
}
