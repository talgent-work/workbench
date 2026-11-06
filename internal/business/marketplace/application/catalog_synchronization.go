package application

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"github.com/google/uuid"
	catalogtask "github.com/talgent-work/workbench/internal/business/marketplace/application/task"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

var ErrSynchronizationDisabled = errors.New("marketplace: synchronization is disabled")

// CatalogSynchronizationPort acquires releases without changing personal installs.
type CatalogSynchronizationPort interface {
	SynchronizeCatalog(context.Context, bool) error
}

// SynchronizationRun is operational metadata, not a Domain state machine.
type SynchronizationRun struct {
	ID                                string
	Status                            string
	Force                             bool
	RequestedAt, Deadline, FinishedAt int64
	Failures                          []SynchronizationFailure
}
type SynchronizationFailure struct {
	Source string `json:"source"`
	Slug   string `json:"slug"`
	Reason string `json:"reason,omitempty"`
}

type SynchronizationRuns interface {
	Reserve(context.Context, SynchronizationRun) (*SynchronizationRun, error)
	Latest(context.Context) (*SynchronizationRun, error)
	Claim(context.Context, string, int64) (*SynchronizationRun, error)
	Finish(context.Context, string, string, []SynchronizationFailure, int64) error
}

// CatalogSynchronization owns shared admission, bounded execution and safe status.
type CatalogSynchronization struct {
	runs         SynchronizationRuns
	synchronizer CatalogSynchronizationPort
	enqueuer     taskqueue.Enqueuer
	timeout      time.Duration
	enabled      bool
	logger       *slog.Logger
}

func NewCatalogSynchronization(runs SynchronizationRuns, synchronizer CatalogSynchronizationPort, enqueuer taskqueue.Enqueuer, timeout time.Duration, enabled bool, logger *slog.Logger) *CatalogSynchronization {
	return &CatalogSynchronization{runs, synchronizer, enqueuer, timeout, enabled, logger}
}
func (a *CatalogSynchronization) Request(ctx context.Context, force bool) (*SynchronizationRun, error) {
	if !a.enabled {
		return nil, ErrSynchronizationDisabled
	}
	now := time.Now()
	run, err := a.runs.Reserve(ctx, SynchronizationRun{ID: uuid.Must(uuid.NewV7()).String(), Status: "queued", Force: force, RequestedAt: now.UnixMilli(), Deadline: now.Add(a.timeout).UnixMilli()})
	if err != nil {
		return nil, err
	}
	if run.Status != "queued" {
		return run, nil
	}
	queued, err := catalogtask.NewSynchronization(run.ID)
	if err != nil {
		return nil, err
	}
	// Retrying a queued reservation also closes the database/Redis crash gap.
	options := []taskqueue.EnqueueOption{taskqueue.WithUnique(a.timeout), taskqueue.WithMaxRetry(0), taskqueue.WithDeadline(time.UnixMilli(run.Deadline)), taskqueue.WithTimeout(a.timeout)}
	if err := taskqueue.NewEnqueueOptions(options...).Validate(); err != nil {
		return nil, err
	}
	if err := a.enqueuer.Enqueue(ctx, queued, options...); err != nil {
		return nil, err
	}
	return run, nil
}
func (a *CatalogSynchronization) Latest(ctx context.Context) (*SynchronizationRun, error) {
	run, err := a.runs.Latest(ctx)
	if run != nil && (run.Status == "queued" || run.Status == "running") && run.Deadline <= time.Now().UnixMilli() {
		run.Status = "timed_out"
	}
	return run, err
}
func (a *CatalogSynchronization) Execute(ctx context.Context, runID string) error {
	if !a.enabled {
		return nil
	}
	if runID == "" {
		_, err := a.Request(ctx, false)
		return err
	}
	run, err := a.runs.Claim(ctx, runID, time.Now().UnixMilli())
	if err != nil || run == nil {
		return err
	}
	bounded, cancel := context.WithDeadline(ctx, time.UnixMilli(run.Deadline))
	defer cancel()
	err = a.synchronizer.SynchronizeCatalog(bounded, run.Force)
	status := "succeeded"
	var failures []SynchronizationFailure
	if err != nil {
		status = "failed"
		if collectSynchronizationFailures(err, &failures) {
			status = "partial_failure"
		}
		if bounded.Err() == context.DeadlineExceeded || status != "partial_failure" && errors.Is(err, context.DeadlineExceeded) {
			status = "timed_out"
		}
	}
	// Task cancellation must not discard its terminal status. The write is bounded.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if saveErr := a.runs.Finish(finishCtx, run.ID, status, failures, time.Now().UnixMilli()); saveErr != nil {
		return errors.New("marketplace: cannot persist synchronization outcome")
	}
	a.logger.InfoContext(ctx, "catalog synchronization publication", "run_id", run.ID, "status", status, "failed_plugins", len(failures))
	for _, failure := range failures {
		a.logger.WarnContext(ctx, "catalog synchronization failure", "run_id", run.ID, "source", failure.Source, "slug", failure.Slug, "reason", failure.Reason)
	}
	// Raw provider errors can contain URLs or secrets; only safe outcomes and
	// diagnoses cross the worker boundary.
	if err != nil {
		return errors.New("marketplace: synchronization " + status)
	}
	return nil
}
func collectSynchronizationFailures(err error, failures *[]SynchronizationFailure) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		partial := true
		for _, child := range joined.Unwrap() {
			if !collectSynchronizationFailures(child, failures) {
				partial = false
			}
		}
		return partial
	}
	if failure, ok := err.(*PluginSyncFailure); ok {
		slug := failure.Slug
		if slug != "" && !pluginconfig.ValidPluginName(slug) {
			slug = "invalid-entry"
		}
		reason := ""
		if failure.Cause != nil {
			reason = SafeDiagnosis(failure.Cause.Error())
		}
		*failures = append(*failures, SynchronizationFailure{Source: failure.Source, Slug: slug, Reason: reason})
		return true
	}
	return false
}
