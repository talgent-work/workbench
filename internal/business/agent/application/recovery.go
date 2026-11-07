package application

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Recovery wakes and performs Agent's retained recovery obligations. A wake
// only enqueues the task for one object; its processor performs one step from
// current state, and the periodic sweep re-wakes every unfinished object.
type Recovery struct {
	app        *Application
	enqueuer   taskqueue.Enqueuer
	executions domain.AgentExecutionRepository
	lookups    query.ExecutionRecoveryReader
	// reconciliation fulfils the steps each execution decides.
	reconciliation *command.ReconcileExecutionHandler
	workspaces     WorkspacePort
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

func NewRecovery(app *Application, enqueuer taskqueue.Enqueuer, executions domain.AgentExecutionRepository, lookups query.ExecutionRecoveryReader, reconciliation *command.ReconcileExecutionHandler, workspaces WorkspacePort, reconnection OwedResumes) *Recovery {
	return &Recovery{app: app, enqueuer: enqueuer, executions: executions, lookups: lookups, reconciliation: reconciliation, workspaces: workspaces, reconnection: reconnection}
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

// WakeExecution wakes the execution's reconciliation.
func (r *Recovery) WakeExecution(ctx context.Context, executionID string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewReconcileAllocation(executionID))
}

// WakeSandboxObservation follows the environment authority's report of a
// change to one of the execution's sandboxes.
func (r *Recovery) WakeSandboxObservation(ctx context.Context, executionID string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewObserveSandbox(executionID))
}

// wakeSteps enqueues each step the execution decided that its own task takes.
func (r *Recovery) wakeSteps(ctx context.Context, executionID string, steps []domain.ExecutionStepKind) error {
	var failures []error
	for _, step := range steps {
		failures = append(failures, r.wakeStep(ctx, executionID, step))
	}
	return errors.Join(failures...)
}

func (r *Recovery) wakeStep(ctx context.Context, executionID string, step domain.ExecutionStepKind) error {
	switch step {
	case domain.StepPrepare:
		return r.enqueue(ctx, preparationAttemptTimeout)(agenttask.NewPrepareAllocation(executionID))
	case domain.StepConnect:
		return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewReconnectAllocation(executionID))
	case domain.StepDeliverCredentials:
		return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewDeliverCredentials(executionID))
	case domain.StepSyncInputs:
		return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewSyncInputs(executionID))
	default:
		return nil
	}
}

// WakeWork wakes the Work's own obligations and those of its current
// execution, whose intents follow the Work's lifecycle.
func (r *Recovery) WakeWork(ctx context.Context, workID string) error {
	err := r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewRecoverWork(workID))
	execution, lookupErr := r.executions.Latest(ctx, workID)
	if errors.Is(lookupErr, domain.ErrExecutionNotFound) || (lookupErr == nil && execution.State == domain.ExecutionReleased) {
		return err
	}
	if lookupErr != nil {
		return errors.Join(err, lookupErr)
	}
	return errors.Join(err, r.WakeExecution(ctx, execution.ID))
}

// ReconnectWork reconnects the Work's launched process after Dispatcher
// reported that its connection ended without a process exit. The reconnect
// neither prepares the environment nor delivers credentials; the report
// changes no Work state.
func (r *Recovery) ReconnectWork(ctx context.Context, workID string) error {
	if strings.TrimSpace(workID) == "" {
		return domain.ErrInvalidWork
	}
	execution, err := r.executions.Latest(ctx, workID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return nil
	}
	if err != nil || execution.Retired() {
		return err
	}
	return r.wakeStep(ctx, execution.ID, domain.StepConnect)
}

func (r *Recovery) WakeContinuation(ctx context.Context, workID string) error {
	return r.enqueue(ctx, recoveryAttemptTimeout)(agenttask.NewRecoverContinuation(workID))
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

// ReconcileExecution decides the execution's steps, takes the short ones and
// wakes the tasks of the others.
func (r *Recovery) ReconcileExecution(ctx context.Context, executionID string) error {
	steps, err := r.reconciliation.Reconcile(ctx, executionID, false)
	return errors.Join(err, r.wakeSteps(ctx, executionID, steps))
}

// ObserveSandbox reconciles the execution against every environment
// carrying its ownership after the environment authority reported a change.
func (r *Recovery) ObserveSandbox(ctx context.Context, executionID string) error {
	steps, err := r.reconciliation.Reconcile(ctx, executionID, true)
	return errors.Join(err, r.wakeSteps(ctx, executionID, steps))
}

func (r *Recovery) PrepareExecution(ctx context.Context, executionID string) error {
	return r.reconciliation.Prepare(ctx, executionID)
}

func (r *Recovery) ConnectExecution(ctx context.Context, executionID string) error {
	return r.reconciliation.Connect(ctx, executionID)
}

func (r *Recovery) DeliverCredentials(ctx context.Context, executionID string) error {
	return r.reconciliation.DeliverCredentials(ctx, executionID)
}

func (r *Recovery) SyncInputs(ctx context.Context, executionID string) error {
	return r.reconciliation.SyncInputs(ctx, executionID)
}

// CredentialChanged follows Marketplace's fact that an authorization's
// credential changed: every delivered execution using it receives the
// current credentials, so Works sharing the authorization change together.
// Agent never polls authorizations. Repeated or reordered facts lead to the
// same delivery of whatever Marketplace supplies now.
func (r *Recovery) CredentialChanged(ctx context.Context, authorizationID string) error {
	if strings.TrimSpace(authorizationID) == "" {
		return domain.ErrInvalidWork
	}
	ids, err := r.lookups.ListExecutionsUsingAuthorization(ctx, authorizationID)
	if err != nil {
		return err
	}
	marked, err := r.reconciliation.MarkCredentialsChanged(ctx, ids)
	failures := []error{err}
	for _, id := range marked {
		failures = append(failures, r.wakeStep(ctx, id, domain.StepDeliverCredentials))
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
	ids, err := r.lookups.ListExecutionsReadingInputs(ctx, owner)
	if err != nil {
		return err
	}
	marked, err := r.reconciliation.MarkInputsChanged(ctx, ids)
	failures := []error{err}
	for _, id := range marked {
		failures = append(failures, r.wakeStep(ctx, id, domain.StepSyncInputs))
	}
	return errors.Join(failures...)
}

// RecoverWork applies what the Work's execution reported and passes the
// Work's intents to it first, so a retained request that keeps failing never
// holds back the report that would settle it, then advances the Work's
// retained control requests and waiting timeout. Each step runs regardless of the others'
// outcome, and every failure is returned together. Accepted input needs no retry here:
// Dispatcher's input buffer delivers it.
func (r *Recovery) RecoverWork(ctx context.Context, workID string) error {
	commands := r.app.Commands
	// The Work's intents and its execution's reports are passed again, so a
	// lost Domain Event is repaired, and native feedback lost on the way is
	// made up by Dispatcher's current observation of the process.
	coordinated := commands.ExecutionCoordination.Coordinate(ctx, workID)
	observed := commands.ApplyExecutionFeedback.Handle(ctx, command.ExecutionFeedback{WorkID: workID, Kind: command.ProcessObserved})
	retained := commands.RecoverWork.Handle(ctx, workID)
	// A close request the Work kept while it stopped applies once stopped; the
	// event that applies it may have been lost.
	kept := commands.CloseWork.ApplyKeptClose(ctx, workID)
	// A Resume owed on its Computer's reconnection is retried here too, so a
	// lost Computer Became Reachable or a failed retry does not wait for the
	// machine's next return.
	var owed error
	if r.reconnection != nil {
		owed = r.reconnection.RetryOwedResume(ctx, workID)
	}
	return errors.Join(coordinated, observed, retained, kept, owed)
}

func (r *Recovery) RecoverContinuation(ctx context.Context, workID string) error {
	return r.app.Commands.ResumeWork.Recover(ctx, workID)
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

// Sweep wakes every unfinished recovery obligation: the reconciliation of
// each unreleased execution, Works holding execution or closure obligations,
// and unsettled requests. Completed history is never listed.
func (r *Recovery) Sweep(ctx context.Context) error {
	queries := r.app.Queries
	return errors.Join(
		sweep(ctx, r.lookups.ListOutstandingExecutions, r.WakeExecution, identity),
		sweep(ctx, pageOf(queries.ListRecoveryWorks.Handle), r.wakeWorkOnly, identity),
		sweep(ctx, pageOf(queries.ListPendingContinuations.Handle), r.WakeContinuation, identity),
		sweep(ctx, pageOf(queries.ListInitialExecutionRecovery.Handle), r.WakeInitialExecution, identity),
		r.app.Commands.ReconcileDesks.Handle(ctx),
	)
}

// The sweep wakes executions on their own, so a Work wake needs no lookup.
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
