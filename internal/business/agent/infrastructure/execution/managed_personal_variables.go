package execution

import (
	"context"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// A Managed run's Personal Variables reach its Harness only (Personal
// Variables at launch only): the Harness's process creation carries them to
// Talgent Computer, which layers them last over the machine's environment,
// the Work's and its own variables, TALGENT_ ones excepted, and keeps them in
// memory only. Neither the Work's environment, which every process of the
// Work gets, nor the Managed specification holds them: the execution keeps
// them sealed apart from it (Run variable sealing). A machine that does not
// acknowledge them never starts the Harness without them (createHarness).

var _ variableDelivery = ManagedEnvironment{}

// relayRequestLimit is the largest request body Computer Gateway's relay
// forwards to a machine.
const relayRequestLimit = 1 << 20

// errVariablesBeyondRelay is the definite failure of a run whose Personal
// Variables do not fit the relay's request: no retry changes them.
var errVariablesBeyondRelay = domain.InvalidLaunchSpecification("the Personal Variables are too large to reach a Managed Computer")

// deliverVariables gives the run's Personal Variables, reserved names left
// out, to the Harness's process creation and names the platform variables
// they replace: those of the Managed specification's environment, those
// Talgent Computer sets for the platform and those the launch command
// exports.
func (ManagedEnvironment) deliverVariables(_ context.Context, record executionRecord, _ *execd.Client, variables []domain.PersonalVariable) (variableHandover, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return variableHandover{}, err
	}
	carried := make(map[string]string, len(variables))
	for _, variable := range variables {
		if !domain.ReservedVariableName(variable.Name) {
			carried[variable.Name] = variable.Value
		}
	}
	size, err := execd.CreationBytes("/workspace", record.LaunchCommand, carried)
	if err != nil {
		return variableHandover{}, oops.With("operation", "measure_personal_variables").Wrap(err)
	}
	if size > relayRequestLimit {
		return variableHandover{}, errVariablesBeyondRelay
	}
	return variableHandover{replaced: replacedVariables(variables, spec.platformVariables()), carried: carried}, nil
}

// platformVariables are the variables the platform sets in a Managed
// Harness's environment: the specification's environment, those Talgent
// Computer sets as the specification asks (the machine's HOME, the model
// proxy's endpoint and token on a Model Configuration, and where telemetry
// goes) and those the launch command exports. The machine's own variables
// are not the platform's.
func (s managedSpecification) platformVariables() map[string]string {
	platform := map[string]string{"HOME": "", "OTEL_EXPORTER_OTLP_ENDPOINT": "", "OTEL_EXPORTER_OTLP_HEADERS": ""}
	for name := range s.Environment {
		platform[name] = ""
	}
	if s.ModelEndpoint != "" {
		platform["ANTHROPIC_BASE_URL"], platform["ANTHROPIC_AUTH_TOKEN"] = "", ""
	}
	for _, variable := range machineLayout.own {
		platform[variable.name] = ""
	}
	return platform
}
