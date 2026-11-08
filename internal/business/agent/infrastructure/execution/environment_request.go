package execution

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var errEnvironmentConflict = errors.New("agent: execution environment conflicts with its recorded execution")

func validateStartRequest(request domain.LaunchSpecification) error {
	if err := request.WorkModel.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(request.WorkID) == "" {
		return domain.ErrExecutionRequest
	}
	if err := request.Capabilities.Validate(); err != nil {
		return err
	}
	if _, err := domain.AgentHarnessByID(request.AgentHarness); err != nil || request.AgentHarness == "" {
		return errors.Join(domain.ErrExecutionRequest, domain.ErrUnknownAgentHarness)
	}
	if strings.TrimSpace(request.HarnessSessionReference) == "" ||
		(request.ContextMode != domain.ContextFresh && request.ContextMode != domain.ContextContinue && request.ContextMode != domain.ContextRecover) {
		return domain.ErrExecutionRequest
	}
	return nil
}

// encodeLaunch stores an execution's LaunchSpecification.
func encodeLaunch(launch domain.LaunchSpecification) ([]byte, error) {
	if err := validateStartRequest(launch); err != nil {
		return nil, err
	}
	data, err := json.Marshal(launch)
	if err != nil {
		return nil, oops.Wrap(err)
	}
	return data, nil
}

// JSON arrays lose their native typed representation at the storage boundary.
// Restore only the supported string-array shape, then revalidate retained facts.
func decodeStartRequest(data []byte) (domain.LaunchSpecification, error) {
	var request domain.LaunchSpecification
	if err := json.Unmarshal(data, &request); err != nil {
		return domain.LaunchSpecification{}, err
	}
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
