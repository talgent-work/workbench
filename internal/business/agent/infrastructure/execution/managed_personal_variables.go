package execution

import (
	"context"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// A Managed run's Personal Variables reach its Harness only: the Harness's
// process creation carries them to Talgent Computer, which layers them last
// over the machine's environment, the Work's and its own variables,
// TALGENT_ ones excepted, keeps them in memory only and names the Work's and
// its own variables they replaced. Neither the Work's environment, which
// every process of the Work gets, nor the Managed specification holds them:
// the execution keeps them sealed apart from it. A machine that does not
// acknowledge them never starts the Harness without them (createHarness),
// and one that lost them before the Harness ever ran, as by restarting, is
// given them again before the Harness connects (restoreVariables).

var _ variableDelivery = ManagedEnvironment{}

// relayRequestLimit is the largest request body Computer Gateway's relay
// forwards to a machine (docs/computer-protocol.md, Relay). The relay's own
// bound lives in the Computer Gateway context, which Agent reaches only
// through the relay's protocol and does not import, so it is restated here.
const relayRequestLimit = 1 << 20

// errVariablesBeyondRelay is the definite failure of a run whose Personal
// Variables do not fit the relay's request: no retry changes them.
var errVariablesBeyondRelay = domain.InvalidLaunchSpecification("the Personal Variables are too large to reach a Managed Computer")

// deliverVariables gives the run's Personal Variables, reserved names left
// out, to the Harness's process creation and names the launch command's own
// exports they replace; Talgent Computer names the rest in its answer.
func (ManagedEnvironment) deliverVariables(_ context.Context, record executionRecord, _ *execd.Client, variables []domain.PersonalVariable) (variableHandover, error) {
	carried := carriedVariables(variables)
	size, err := execd.CreationBytes(domain.WorkspacePath, record.LaunchCommand, carried)
	if err != nil {
		return variableHandover{}, oops.With("operation", "measure_personal_variables").Wrap(err)
	}
	if size > relayRequestLimit {
		return variableHandover{}, errVariablesBeyondRelay
	}
	return variableHandover{replaced: replacedVariables(variables, nil, machineLayout.exports()), carried: carried}, nil
}

// withdrawVariables has nothing to remove: the values travelled in the
// failed process creation itself, and Talgent Computer holds a reserved
// process's values in memory only, ending with its deletion.
func (ManagedEnvironment) withdrawVariables(context.Context, executionRecord, *execd.Client) error {
	return nil
}

// restoreVariables gives the launched Harness process the run's Personal
// Variables again when its Talgent Computer awaits them: it restarted after
// the process was reserved and before it ever ran, losing the values it
// held in memory only. Until then the process neither starts nor fails.
func (ManagedEnvironment) restoreVariables(ctx context.Context, record executionRecord, client *execd.Client, variables []domain.PersonalVariable) error {
	awaiting, err := client.AwaitsVariables(ctx, record.ProcessID)
	if err != nil {
		return oops.With("operation", "observe_personal_variables").Wrap(err)
	}
	if !awaiting {
		return nil
	}
	if err := client.RestoreVariables(ctx, record.ProcessID, carriedVariables(variables)); err != nil {
		return oops.With("operation", "restore_personal_variables").Wrap(err)
	}
	return nil
}
