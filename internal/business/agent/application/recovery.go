package application

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Recovery wakes and performs Agent's retained recovery obligations. A wake
// only enqueues the task for one object; its processor performs one step from
// current state, and the periodic sweep re-wakes every unfinished object.
type Recovery struct {
	app          *Application
	enqueuer     taskqueue.Enqueuer
	environments command.ExecutionEnvironmentPort
	workspaces   WorkspacePort
	// reconnection retries the Resume a marked Work is owed while its
	// Managed Computer is reachable; nil where no Managed Computer is
	// reached.
	reconnection OwedResumes
}

// OwedResumes retries the Resume a Work marked ReachResumeDue is owed once
// its Managed Computer is reachable (Retry on reconnection).
type OwedResumes interface {
	RetryOwedResume(ctx context.Context, workID string) error
}

func NewRecovery(app *Application, enqueuer taskqueue.Enqueuer, environments command.ExecutionEnvironmentPort, workspaces WorkspacePort, reconnection OwedResumes) *Recovery {
	return &Recovery{app: app, enqueuer: enqueuer, environments: environments, workspaces: workspaces, reconnection: reconnection}
}

// RequestWorkspaceReclamation queues the Closed Work's one reclamation
// request, so it reaches Storage even across restarts and outages. Agent
// keeps no reclamation obligation: the queue only delivers the request.
func (r *Recovery) RequestWorkspaceReclamation(ctx context.Context, workID, workspaceReference string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewRequestWorkspaceReclamation(workID, workspaceReference))
}

// SendWorkspaceReclamation delivers a queued request to Storage.
func (r *Recovery) SendWorkspaceReclamation(ctx context.Context, workID, workspaceReference string) error {
	return r.workspaces.RequestWorkspaceReclamation(ctx, workID, workspaceReference)
}

// Recovery never gives up: a removal or closure obligation cannot be abandoned.
// The worker's retry delay bounds how often a failing object is retried.
const unlimitedRetries = math.MaxInt32

const (
	recoveryAttemptTimeout    = 2 * time.Minute
	preparationAttemptTimeout = domain.StartupDeadline + time.Minute
)

func (r *Recovery) WakeAllocation(ctx context.Context, allocationID string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewReconcileAllocation(allocationID))
}

// WakeSandboxObservation follows the environment authority's report of a
// change to one of the allocation's sandboxes.
func (r *Recovery) WakeSandboxObservation(ctx context.Context, allocationID string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewObserveSandbox(allocationID))
}

// wakeStep enqueues the step an allocation needs next.
func (r *Recovery) wakeStep(ctx context.Context, allocationID string, step command.AllocationStep) error {
	switch step {
	case command.AllocationReconcile:
		return r.WakeAllocation(ctx, allocationID)
	case command.AllocationPrepare:
		return r.enqueue(ctx, preparationAttemptTimeout)(agenttask.NewPrepareAllocation(allocationID))
	case command.AllocationReconnect:
		return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewReconnectAllocation(allocationID))
	case command.AllocationDeliver:
		return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewDeliverCredentials(allocationID))
	case command.AllocationSyncInputs:
		return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewSyncInputs(allocationID))
	default:
		return nil
	}
}

// WakeWork wakes the Work's own obligations and those of its live allocation,
// whose credentials and removal follow the Work's lifecycle.
func (r *Recovery) WakeWork(ctx context.Context, workID string) error {
	err := r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewRecoverWork(workID))
	allocation, found, lookupErr := r.environments.LiveAllocation(ctx, workID)
	if lookupErr != nil || !found {
		return errors.Join(err, lookupErr)
	}
	return errors.Join(err, r.WakeAllocation(ctx, allocation))
}

// ReconnectWork reconnects the Work's live allocation after Dispatcher reported
// that its connection ended without a process exit. The reconnect neither
// prepares the environment nor delivers credentials; the report changes no
// Work state.
func (r *Recovery) ReconnectWork(ctx context.Context, workID string) error {
	if strings.TrimSpace(workID) == "" {
		return domain.ErrInvalidWork
	}
	allocation, found, err := r.environments.LiveAllocation(ctx, workID)
	if err != nil || !found {
		return err
	}
	return r.wakeStep(ctx, allocation, command.AllocationReconnect)
}

func (r *Recovery) WakeContinuation(ctx context.Context, workID string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewRecoverContinuation(workID))
}

func (r *Recovery) WakeWorkCreation(ctx context.Context, requestID string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewRecoverWorkCreation(requestID))
}

func (r *Recovery) WakeInitialExecution(ctx context.Context, workID string) error {
	return r.enqueue(ctx, preparationAttemptTimeout)(agenttask.NewPrepareInitialExecution(workID))
}

// WakeSweep wakes every unfinished obligation now, as on process start, instead
// of waiting for the next periodic sweep.
func (r *Recovery) WakeSweep(ctx context.Context, interval time.Duration) error {
	task, err := agenttask.NewSweepRecovery()
	if err != nil {
		return err
	}
	return r.enqueuer.Enqueue(ctx, task, SweepPolicy(interval)...)
}

// SweepPolicy runs a sweep once, never retries it, and lets one sweep start
// per interval however many replicas fire it. A failed sweep is replaced by the
// next one.
func SweepPolicy(interval time.Duration) []taskqueue.EnqueueOption {
	return []taskqueue.EnqueueOption{taskqueue.WithMaxRetry(0), taskqueue.WithTimeout(interval), taskqueue.WithUnique(max(interval*9/10, time.Second))}
}

func (r *Recovery) enqueue(ctx context.Context, timeout time.Duration) func(taskqueue.Task, error) error {
	return func(task taskqueue.Task, err error) error {
		if err != nil {
			return err
		}
		options := []taskqueue.EnqueueOption{taskqueue.WithMaxRetry(unlimitedRetries), taskqueue.WithTimeout(timeout)}
		if err := taskqueue.NewEnqueueOptions(options...).Validate(); err != nil {
			return err
		}
		return r.enqueuer.Enqueue(ctx, task, options...)
	}
}

func (r *Recovery) ReconcileAllocation(ctx context.Context, allocationID string) error {
	next, err := r.environments.ReconcileAllocation(ctx, allocationID)
	if err != nil {
		return err
	}
	return r.wakeStep(ctx, allocationID, next)
}

func (r *Recovery) PrepareAllocation(ctx context.Context, allocationID string) error {
	return r.environments.PrepareAllocation(ctx, allocationID)
}

func (r *Recovery) ReconnectAllocation(ctx context.Context, allocationID string) error {
	return r.environments.ReconnectAllocation(ctx, allocationID)
}

func (r *Recovery) DeliverCredentials(ctx context.Context, allocationID string) error {
	return r.environments.DeliverCredentials(ctx, allocationID)
}

func (r *Recovery) SyncInputs(ctx context.Context, allocationID string) error {
	return r.environments.SyncInputs(ctx, allocationID)
}

// CredentialChanged follows Marketplace's fact that an authorization's
// credential changed: every delivered allocation using it receives the
// current credentials, so Works sharing the authorization change together.
// Agent never polls authorizations. Repeated or reordered facts lead to the
// same delivery of whatever Marketplace supplies now.
func (r *Recovery) CredentialChanged(ctx context.Context, authorizationID string) error {
	if strings.TrimSpace(authorizationID) == "" {
		return domain.ErrInvalidWork
	}
	allocations, err := r.environments.MarkCredentialsChanged(ctx, authorizationID)
	if err != nil {
		return err
	}
	var failures []error
	for _, allocation := range allocations {
		failures = append(failures, r.wakeStep(ctx, allocation, command.AllocationDeliver))
	}
	return errors.Join(failures...)
}

// InputsChanged follows Storage's fact that a file in owner's File Space
// changed: every Work on a Managed Computer whose input source is that space
// receives its current inputs by their own sync, which this wakes. A Cloud
// Work mounts its inputs and needs nothing. Repeated or reordered facts lead
// to the same sync of the current files.
func (r *Recovery) InputsChanged(ctx context.Context, owner string) error {
	if strings.TrimSpace(owner) == "" {
		return domain.ErrInvalidWork
	}
	allocations, err := r.environments.MarkInputsChanged(ctx, owner)
	if err != nil {
		return err
	}
	var failures []error
	for _, allocation := range allocations {
		failures = append(failures, r.wakeStep(ctx, allocation, command.AllocationSyncInputs))
	}
	return errors.Join(failures...)
}

func (r *Recovery) ObserveSandbox(ctx context.Context, allocationID string) error {
	next, err := r.environments.ObserveSandbox(ctx, allocationID)
	if err != nil {
		return err
	}
	return r.wakeStep(ctx, allocationID, next)
}

// RecoverWork advances the Work's Startup Deadline, retained control request and
// waiting timeout, and settles exit and release from current observation, in
// that order. Accepted input needs no
// retry here: Dispatcher's input buffer delivers it.
func (r *Recovery) RecoverWork(ctx context.Context, workID string) error {
	commands := r.app.Commands
	if err := commands.RecoverWork.Handle(ctx, workID); err != nil {
		return err
	}
	// Native feedback is best effort. Current observation settles exits and
	// releases whose notification was lost.
	if err := commands.ApplyExecutionFeedback.Handle(ctx, command.ExecutionFeedback{WorkID: workID, Kind: command.ProcessObserved}); err != nil {
		return err
	}
	// A close request the Work kept while it stopped applies once stopped; the
	// event that applies it may have been lost.
	if err := commands.CloseWork.ApplyKeptClose(ctx, workID); err != nil {
		return err
	}
	// A Resume owed on its Computer's reconnection is retried here too, so a
	// lost Computer Became Reachable or a failed retry does not wait for the
	// machine's next return.
	if r.reconnection == nil {
		return nil
	}
	return r.reconnection.RetryOwedResume(ctx, workID)
}

func (r *Recovery) RecoverContinuation(ctx context.Context, workID string) error {
	return r.app.Commands.ResumeWork.Recover(ctx, workID)
}

// RecoverWorkCreation settles a retained creation request; a cancelled or
// rejected request is a settled outcome, not a failure to retry.
func (r *Recovery) RecoverWorkCreation(ctx context.Context, requestID string) error {
	err := r.app.Commands.CreateRequestedWork.Recover(ctx, requestID)
	if errors.Is(err, command.ErrWorkCreationCanceled) || errors.Is(err, command.ErrInitialExecutionRejected) {
		return nil
	}
	return err
}

// PrepareInitialExecution advances the first start's preparation and input
// delivery. A first start waiting for readiness or for its next admitted start
// is woken again by that Work's commit; a Work without a first-start
// obligation has nothing to prepare.
func (r *Recovery) PrepareInitialExecution(ctx context.Context, workID string) error {
	err := r.app.Commands.RecoverInitialExecution.Handle(ctx, workID)
	if errors.Is(err, command.ErrInitialExecutionPending) || errors.Is(err, command.ErrInitialExecutionNotFound) {
		return nil
	}
	return err
}

const sweepPage = 100

// Sweep wakes every unfinished recovery obligation: the one missing step of
// each unfinished allocation, Works holding execution or closure obligations,
// and unsettled requests. A connected allocation whose stream Dispatcher still
// owns is not woken. Completed history is never listed.
func (r *Recovery) Sweep(ctx context.Context) error {
	queries := r.app.Queries
	return errors.Join(
		sweep(ctx, r.environments.ListOutstandingAllocations, func(ctx context.Context, status command.AllocationStatus) error {
			return r.wakeStep(ctx, status.AllocationID, status.Step)
		}, func(status command.AllocationStatus) string { return status.AllocationID }),
		sweep(ctx, pageOf(queries.ListRecoveryWorks.Handle), r.wakeWorkOnly, identity),
		sweep(ctx, pageOf(queries.ListPendingContinuations.Handle), r.WakeContinuation, identity),
		sweep(ctx, pageOf(queries.ListWorkCreationRecovery.Handle), r.WakeWorkCreation, identity),
		sweep(ctx, pageOf(queries.ListInitialExecutionRecovery.Handle), r.WakeInitialExecution, identity),
		r.app.Commands.ReconcileDesks.Handle(ctx),
	)
}

// The sweep wakes allocations on their own, so a Work wake needs no lookup.
func (r *Recovery) wakeWorkOnly(ctx context.Context, workID string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewRecoverWork(workID))
}

func pageOf(list func(context.Context, string) ([]string, error)) func(context.Context, string, int) ([]string, error) {
	return func(ctx context.Context, after string, _ int) ([]string, error) { return list(ctx, after) }
}

func identity(id string) string { return id }

// sweep wakes each listed object; one failed wake does not stop the others.
// Paging ends on an empty page, so it does not depend on a list's page size.
func sweep[T any](ctx context.Context, list func(context.Context, string, int) ([]T, error), wake func(context.Context, T) error, key func(T) string) error {
	var failures []error
	after := ""
	for {
		ids, err := list(ctx, after, sweepPage)
		// A page judged in part still wakes what it could judge.
		failures = append(failures, err)
		if len(ids) == 0 {
			return errors.Join(failures...)
		}
		for _, id := range ids {
			if err := wake(ctx, id); err != nil {
				failures = append(failures, err)
			}
			after = key(id)
		}
	}
}
