package eventhandler

import (
	"context"
	"log/slog"

	domainevent "github.com/go-jimu/components/ddd/event"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// The Policies between a Work and its AgentExecution. Each handler applies
// the current facts of both Roots for the Work its event names, so a repeated
// event changes nothing; a reaction that fails is logged and left to the
// Work's recovery, which the recovery sweep runs for every unfinished Work.

// BeginExecution begins the AgentExecution of an admitted start (Begin
// execution).
type BeginExecution struct{ coordination }

// RequestProcessExit records a Work's request that its process exit on its
// execution (Request process exit).
type RequestProcessExit struct{ coordination }

// RetireExecution retires a Work's execution once its execution ended
// (Retire execution).
type RetireExecution struct{ coordination }

// ApplyExecutionReport applies an execution's report of its run to its Work
// (Apply execution report).
type ApplyExecutionReport struct{ coordination }

// ConfirmComputeRelease applies an execution's release to its Work (Confirm
// compute release).
type ConfirmComputeRelease struct{ coordination }

var (
	_ domainevent.Handler = (*BeginExecution)(nil)
	_ domainevent.Handler = (*RequestProcessExit)(nil)
	_ domainevent.Handler = (*RetireExecution)(nil)
	_ domainevent.Handler = (*ApplyExecutionReport)(nil)
	_ domainevent.Handler = (*ConfirmComputeRelease)(nil)
)

func NewBeginExecution(c *command.ExecutionCoordination, logger *slog.Logger) *BeginExecution {
	return &BeginExecution{coordination{policy: "begin_execution", apply: c.BeginExecution, logger: logger}}
}

func NewRequestProcessExit(c *command.ExecutionCoordination, logger *slog.Logger) *RequestProcessExit {
	return &RequestProcessExit{coordination{policy: "request_process_exit", apply: c.RequestProcessExit, logger: logger}}
}

func NewRetireExecution(c *command.ExecutionCoordination, logger *slog.Logger) *RetireExecution {
	return &RetireExecution{coordination{policy: "retire_execution", apply: c.RetireExecution, logger: logger}}
}

func NewApplyExecutionReport(c *command.ExecutionCoordination, logger *slog.Logger) *ApplyExecutionReport {
	return &ApplyExecutionReport{coordination{policy: "apply_execution_report", apply: c.ApplyExecutionReports, logger: logger}}
}

func NewConfirmComputeRelease(c *command.ExecutionCoordination, logger *slog.Logger) *ConfirmComputeRelease {
	return &ConfirmComputeRelease{coordination{policy: "confirm_compute_release", apply: c.ApplyExecutionReports, logger: logger}}
}

func (*BeginExecution) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindWorkStartAdmitted}
}

func (*RequestProcessExit) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindWorkStopRequested}
}

func (*RetireExecution) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindWorkComputeReleaseRequested}
}

func (*ApplyExecutionReport) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindExecutionReady, domain.EventKindExecutionStartFailed, domain.EventKindExecutionStartRejected, domain.EventKindExecutionStartAbandoned, domain.EventKindExecutionEnvironmentLost, domain.EventKindExecutionExited}
}

func (*ConfirmComputeRelease) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindExecutionReleased}
}

// coordination applies one Policy to the Work its event names.
type coordination struct {
	policy string
	apply  func(context.Context, string) error
	logger *slog.Logger
}

// keyed is every request and report: each names its Work.
type keyed interface{ Key() string }

func (c coordination) Handle(ctx context.Context, event domainevent.Event) {
	named, ok := event.(keyed)
	if !ok {
		return
	}
	if err := c.apply(ctx, named.Key()); err != nil {
		c.logger.WarnContext(ctx, "execution policy left to the Work's recovery", slog.String("policy", c.policy), slog.String("agent_work_id", named.Key()), slog.Any("error", err))
	}
}
