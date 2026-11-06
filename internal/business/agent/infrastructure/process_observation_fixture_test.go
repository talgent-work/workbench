package infrastructure_test

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
)

// observedProcess stands in for the environment authority; every named
// process is the Work's current one. Exit facts carry their own outcome.
type observedProcess agentdomain.ProcessObservation

func (p observedProcess) ObserveProcess(context.Context, string) (command.WorkProcessObservation, error) {
	return command.WorkProcessObservation{ProcessObservation: agentdomain.ProcessObservation(p)}, nil
}

func (observedProcess) IsCurrentProcess(_ context.Context, _ string, processID string) (bool, error) {
	return processID != "", nil
}

func (observedProcess) AcknowledgeExit(_ context.Context, _ string, processID string) (bool, error) {
	return processID != "", nil
}

var exitedProcess = observedProcess{State: agentdomain.ProcessPending, ComputeReclaimed: true}

// workRef keeps a started Work addressable after a start command that returns
// only whether it was admitted; a Work has at most one Harness process.
func workRef(workID string) func(bool, error) (string, error) {
	return func(_ bool, err error) (string, error) { return workID, err }
}

func continuedRef(workID string, err error) (string, error) { return workID, err }
