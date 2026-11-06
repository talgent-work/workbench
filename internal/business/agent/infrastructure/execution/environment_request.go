package execution

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var errEnvironmentConflict = errors.New("agent: execution environment conflicts with its recorded allocation")

// errAllocationNotHeld means the allocation no longer backs a Work process that
// holds its sandbox; its credential and native connection grant nothing.
var errAllocationNotHeld = errors.New("agent: allocation no longer backs the Work's Harness process")

func validateStartRequest(request domain.StartExecutionRequest) error {
	if err := request.WorkModel.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(request.WorkID) == "" {
		return domain.ErrExecutionRequest
	}
	if err := request.Capabilities.Validate(); err != nil {
		return err
	}
	if err := request.HomeBinding.Validate(); err != nil {
		return errors.Join(domain.ErrExecutionRequest, err)
	}
	if strings.TrimSpace(request.HarnessSessionReference) == "" ||
		(request.ContextMode != domain.ContextFresh && request.ContextMode != domain.ContextContinue && request.ContextMode != domain.ContextRecover) {
		return domain.ErrExecutionRequest
	}
	return nil
}

// allocationRequest is the stored admitted start of an allocation and the
// source of its fingerprint. It holds what identifies the start; bounds used
// only while registering, such as Plugin preparation's, stay out so that a
// repeated registration of the same start matches. A Managed Work's
// permission mode stays out too: the launch command already carries it, and
// a change while the Work runs must not make a repeated registration differ.
type allocationRequest struct {
	RepositorySelection     domain.WorkRepositorySelection
	AuthorizationSelection  domain.WorkAuthorizationSelection
	ThinkingLevel           string `json:",omitempty"`
	WorkModel               allocationWorkModel
	AgentID                 string
	WorkID                  string
	Capabilities            allocationCapabilities
	HarnessSessionReference string
	ContextMode             domain.ContextMode
	WorkspaceReference      string
	// InputSource is kept so every credential delivery, also after a
	// restart, can hand the Space Token to the sandbox's Vault. The token is
	// stored as the Work itself stores it (agent_session), in the same
	// trusted database; it admits writes into this one File Space only, and
	// only alongside a current Work credential.
	InputSource     domain.WorkInputSource `json:",omitzero"`
	ReadOnlyMounts  []domain.ReadOnlyMount `json:",omitempty"`
	HomeBinding     domain.HomeBinding
	TraceAttributes map[string]string
}

// allocationWorkModel is the stored model choice of a start. The machine's
// own login is left out while off, so every start that does not choose it
// keeps the fingerprint it had before a Work could; otherwise a live
// allocation registered by an earlier release would conflict with its own
// repeated registration.
type allocationWorkModel struct {
	ConfigurationID string
	MachineLogin    bool `json:",omitempty"`
}

// allocationCapabilities is the stored capability snapshot of a start. The
// sandbox is left out while off, so every start that does not ask for it
// keeps the fingerprint it had before a Work could; otherwise a live
// allocation registered by an earlier release would conflict with its own
// repeated registration.
type allocationCapabilities struct {
	Prompt       string
	StartupAgent string
	Plugins      []domain.SelectedPlugin
	Connectors   []domain.SelectedConnector
	Sandbox      bool `json:",omitempty"`
}

func allocationCapabilitiesFrom(snapshot domain.CapabilitySnapshot) allocationCapabilities {
	snapshot = snapshot.Clone()
	return allocationCapabilities{Prompt: snapshot.Prompt, StartupAgent: snapshot.StartupAgent, Plugins: snapshot.Plugins, Connectors: snapshot.Connectors, Sandbox: snapshot.Sandbox}
}

func (c allocationCapabilities) snapshot() domain.CapabilitySnapshot {
	return domain.CapabilitySnapshot{Prompt: c.Prompt, StartupAgent: c.StartupAgent, Plugins: c.Plugins, Connectors: c.Connectors, Sandbox: c.Sandbox}
}

func allocationRequestFromStart(request domain.StartExecutionRequest) allocationRequest {
	return allocationRequest{RepositorySelection: request.RepositorySelection, AuthorizationSelection: request.AuthorizationSelection, ThinkingLevel: request.ThinkingLevel, WorkModel: allocationWorkModel(request.WorkModel), AgentID: request.AgentID, WorkID: request.WorkID,
		Capabilities: allocationCapabilitiesFrom(request.Capabilities), HarnessSessionReference: request.HarnessSessionReference, ContextMode: request.ContextMode, WorkspaceReference: request.WorkspaceReference, InputSource: request.InputSource, ReadOnlyMounts: request.ReadOnlyMounts, HomeBinding: request.HomeBinding, TraceAttributes: request.TraceAttributes}
}

func (r allocationRequest) start() domain.StartExecutionRequest {
	return domain.StartExecutionRequest{RepositorySelection: r.RepositorySelection, AuthorizationSelection: r.AuthorizationSelection, ThinkingLevel: r.ThinkingLevel, WorkModel: domain.WorkModelSelection(r.WorkModel), AgentID: r.AgentID, WorkID: r.WorkID,
		Capabilities: r.Capabilities.snapshot(), HarnessSessionReference: r.HarnessSessionReference, ContextMode: r.ContextMode, WorkspaceReference: r.WorkspaceReference, InputSource: r.InputSource, ReadOnlyMounts: r.ReadOnlyMounts, HomeBinding: r.HomeBinding, TraceAttributes: r.TraceAttributes}
}

// JSON arrays lose their native typed representation at the storage boundary.
// Restore only the supported string-array shape, then revalidate retained facts.
func decodeStartRequest(data []byte) (domain.StartExecutionRequest, error) {
	var stored allocationRequest
	if err := json.Unmarshal(data, &stored); err != nil {
		return domain.StartExecutionRequest{}, err
	}
	request := stored.start()
	for i := range request.Capabilities.Plugins {
		plugin := &request.Capabilities.Plugins[i]
		plugin.Configuration = pluginconfig.FromJSON(plugin.Configuration)
		for name, declaration := range plugin.Release.Parameters {
			declaration.Default = pluginconfig.JSONValue(declaration.Default)
			plugin.Release.Parameters[name] = declaration
		}
	}
	return request, validateStartRequest(request)
}
