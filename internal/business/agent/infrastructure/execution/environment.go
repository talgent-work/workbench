package execution

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type EnvironmentResources interface {
	// Create starts the environment with the Mount Grants Storage issued.
	Create(context.Context, domain.StartExecutionRequest, domain.SandboxMounts) error
	// CreateManaged starts the environment of a Work on a Managed Computer,
	// which holds no mounts.
	CreateManaged(context.Context, domain.StartExecutionRequest) error
	Delete(ctx context.Context, workID string) error
	// RetireFenced retires the Work's live allocation once its Managed
	// Computer fenced the run, without reclaiming compute.
	RetireFenced(ctx context.Context, workID string) error
	// StartRejected reports whether the Work's live allocation is a rejected
	// start, which holds no process and no compute.
	StartRejected(ctx context.Context, workID string) (bool, error)
	// StartAbandoned reports whether the Work's live allocation never launched
	// a Harness process and no longer can.
	StartAbandoned(ctx context.Context, workID string) (bool, error)
}

// Environment fulfills AgentWork requests through same-BC resource integration
// and Dispatcher's native execution protocol. Request results are not lifecycle facts.
//
// It realizes the execution port for every Computer alike: a
// start is routed by its Computer's kind, a Cloud's into a sandbox with the
// mounts Storage grants, a Managed Computer's into the Work's directory on
// the machine with none. Both become one allocation that the same recovery
// carries through submission, preparation, launch and Connect, asking the
// realization's own environment authority at each step; Stop, Cancel and
// reclamation then follow the allocation.
type Environment struct {
	resources  EnvironmentResources
	dispatcher *DispatcherControl
	mounts     domain.SandboxMountPort
	computers  command.ComputerDescriptionPort
}

var _ domain.ExecutionPort = (*Environment)(nil)

func NewEnvironment(resources EnvironmentResources, dispatcher *DispatcherControl, mounts domain.SandboxMountPort, computers command.ComputerDescriptionPort) (*Environment, error) {
	if resources == nil || dispatcher == nil || mounts == nil || computers == nil {
		return nil, errors.New("agent: environment resources, Dispatcher control, sandbox mounts and Computer lookup are required")
	}
	return &Environment{resources: resources, dispatcher: dispatcher, mounts: mounts, computers: computers}, nil
}

// StartExecution carries out Start execution. On a Cloud, Storage grants the
// start's HOME, Session Workspace and input source, creating or reusing each,
// and the environment is created with exactly those mounts; a Work on a
// Managed Computer acquires none.
func (e *Environment) StartExecution(ctx context.Context, input domain.StartExecutionRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStartRequest(input); err != nil {
		return err
	}
	place, err := e.computers.DescribeComputer(ctx, input.ComputerID)
	if err != nil {
		return err
	}
	input.ComputerName = place.Name
	if place.Managed {
		return e.resources.CreateManaged(ctx, input)
	}
	mounts, err := e.mounts.AcquireMounts(ctx, input.MountRequest())
	if err != nil {
		return err
	}
	return e.resources.Create(ctx, input, mounts)
}

// StopExecution asks the Work's process to exit. A rejected start never had a
// process, so its stop is already complete; asking Dispatcher would fail for
// lack of a connection and hold recovery before it applies the rejection.
func (e *Environment) StopExecution(ctx context.Context, workID, stopID string) error {
	rejected, err := e.resources.StartRejected(ctx, workID)
	if err != nil || rejected {
		return err
	}
	// Dispatcher can stop only a process that connected; a start that never
	// launched one has nothing to stop, and observation reports it abandoned.
	abandoned, err := e.resources.StartAbandoned(ctx, workID)
	if err != nil || abandoned {
		return err
	}
	return e.dispatcher.StopExecution(ctx, workID, stopID)
}

// CancelRunningTurn ends the Turn the Harness is running, if one is; asking again
// while its cancellation is pending, or after it ended, changes nothing.
func (e *Environment) CancelRunningTurn(ctx context.Context, workID string) error {
	turn, err := e.dispatcher.GetCurrentTurn(ctx, workID)
	if err != nil || !turn.Active || turn.ID == "" || turn.CancelRequested {
		return err
	}
	err = e.dispatcher.CancelTurn(ctx, workID, turn.ID)
	if errors.Is(err, domain.ErrWorkTransition) {
		// The Turn ended before the cancellation reached it.
		return nil
	}
	return err
}

func (e *Environment) ReclaimCompute(ctx context.Context, workID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.resources.Delete(ctx, workID)
}

// RetireFencedRun retires what the platform keeps for a run its Managed
// Computer fenced; no compute is reclaimed.
func (e *Environment) RetireFencedRun(ctx context.Context, workID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.resources.RetireFenced(ctx, workID)
}
