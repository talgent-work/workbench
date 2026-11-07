package command

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// longStep reports a step that may wait minutes: the creation's submission,
// which waits for the environment to be ready or to fail, and preparation.
// Such a step is taken by RunLongStep on its own workers, so it never delays
// the short steps of the same or any other execution.
func longStep(kind domain.ExecutionStepKind) bool {
	return kind == domain.StepSubmitCreation || kind == domain.StepPrepare
}

// launchTimeout bounds starting the Harness and connecting it.
const launchTimeout = 30 * time.Second

// longStepsPerRun bounds the long steps one run takes: the creation's
// submission, then the preparation that a reconciliation made due while the
// creation awaited its answer. That reconciliation's wake of the long step
// found this run still active and was absorbed, so this run decides again.
const longStepsPerRun = 2

// RunLongStep takes the long steps the execution's current facts call for, if
// any, deciding again after each; a step no longer decided is not taken.
func (h *ReconcileExecutionHandler) RunLongStep(ctx context.Context, executionID string) error {
	for range longStepsPerRun {
		execution, decision, _, err := h.decide(ctx, executionID)
		if err != nil || execution == nil {
			return err
		}
		i := slices.IndexFunc(decision.Steps, func(step domain.ExecutionStep) bool { return longStep(step.Kind) })
		if i < 0 {
			return nil
		}
		switch decision.Steps[i].Kind {
		case domain.StepSubmitCreation:
			if err := h.submit(ctx, execution); err != nil {
				return err
			}
		case domain.StepPrepare:
			return h.prepare(ctx, execution)
		}
	}
	return nil
}

// submit fixes the environment's specification with the Mount Grants
// Storage issues on a Cloud, records the submission, and only then submits
// the creation once, waiting for its answer (Single creation). Its answer is
// recorded: a created environment is bound unless one is already, and a
// definite refusal or failure rejects a start that bound nothing (Creation
// refusal). A lost answer is returned and the creation is never sent again;
// observation and the Startup Deadline decide the start (Lost creation
// answer).
func (h *ReconcileExecutionHandler) submit(ctx context.Context, execution *domain.AgentExecution) error {
	var mounts domain.SandboxMounts
	if execution.Realization == domain.RealizationCloud {
		var err error
		if mounts, err = h.mounts.AcquireMounts(ctx, execution.Launch.MountRequest()); err != nil {
			return err
		}
	}
	if err := h.environments.PrepareCreation(ctx, execution, mounts); err != nil {
		return err
	}
	if !execution.RecordSubmission() {
		return nil
	}
	if err := h.executions.Save(ctx, execution); err != nil {
		return err
	}
	submitted, err := h.executions.Get(ctx, execution.ID)
	if err != nil {
		return err
	}
	answer, err := h.environments.SubmitCreation(ctx, submitted)
	if err != nil {
		return err
	}
	return h.record(ctx, submitted.ID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordCreationAnswer(answer)
	})
}

// prepare sees the in-environment preparation through to its exit, no later
// than the Startup Deadline, recording its Plugins' installation with its
// end, then launches the Harness and connects it, each outcome recorded
// before the next. A start past its deadline launches
// nothing; the execution records its failure.
func (h *ReconcileExecutionHandler) prepare(ctx context.Context, execution *domain.AgentExecution) error {
	executionID := execution.ID
	deadline := execution.StartupDeadlineAt()
	if !h.now().Before(deadline) {
		return nil
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if !execution.Process.PreparationEnded {
		installations, err := h.environments.Prepare(bounded, execution)
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			// The Startup Deadline passed while it prepared; the execution's
			// next reconciliation records the failure.
			return nil
		}
		if err != nil {
			return err
		}
		if err := h.record(ctx, executionID, func(execution *domain.AgentExecution) (bool, error) {
			return execution.RecordPreparation(domain.PreparationReport{Installations: installations})
		}); err != nil {
			return err
		}
		if execution, err = h.executions.Get(ctx, executionID); err != nil {
			return err
		}
	}
	launch, release := context.WithTimeout(ctx, launchTimeout)
	defer release()
	processID, err := h.environments.Launch(launch, execution)
	if err != nil {
		return err
	}
	launched := false
	if err := h.record(ctx, executionID, func(execution *domain.AgentExecution) (bool, error) {
		changed, err := execution.RecordLaunch(processID)
		launched = changed
		return changed, err
	}); err != nil || !launched {
		// An uncertain or refused record leaves the launched process to its
		// environment's deletion; it is never connected.
		return err
	}
	if execution, err = h.executions.Get(ctx, executionID); err != nil {
		return err
	}
	return h.environments.Connect(launch, execution)
}
