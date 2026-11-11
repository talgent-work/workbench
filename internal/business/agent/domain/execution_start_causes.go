package domain

import (
	"errors"
	"strings"
)

// LaunchSpecificationError is a definite failure of an admitted start's own
// LaunchSpecification, such as a retained Plugin release that cannot be
// installed: no retry can change it. Reason names the cause; it carries
// nothing secret, since the Work's failure shows it to its owners.
type LaunchSpecificationError struct {
	Reason string
}

// InvalidLaunchSpecification is the definite failure of a LaunchSpecification
// for reason.
func InvalidLaunchSpecification(reason string) error {
	return &LaunchSpecificationError{Reason: reason}
}

func (e *LaunchSpecificationError) Error() string {
	return ErrExecutionRequest.Error() + ": " + e.Reason
}

// Unwrap keeps it an execution request failure.
func (e *LaunchSpecificationError) Unwrap() error { return ErrExecutionRequest }

// startCauseLimit bounds a recorded cause, so a failure reason that carries
// it stays within what its Work retains.
const startCauseLimit = 320

// DefiniteStartCause reports whether a failed attempt at a startup step is
// definite, so no retry can change it, and the cause that names it: a
// LaunchSpecificationError's reason, or the request error it is.
func DefiniteStartCause(err error) (string, bool) {
	var invalid *LaunchSpecificationError
	if errors.As(err, &invalid) {
		return startCause(invalid.Reason), true
	}
	for _, definite := range []error{ErrModelConfigurationWithdrawn, ErrUnknownAgentHarness, ErrInvalidCapabilities, ErrExecutionRequest} {
		if errors.Is(err, definite) {
			return startCause(strings.TrimPrefix(definite.Error(), "agent: ")), true
		}
	}
	return "", false
}

// startCause is a cause as recorded: trimmed, without a closing period, and
// within startCauseLimit.
func startCause(cause string) string {
	cause = strings.TrimSuffix(strings.TrimSpace(cause), ".")
	if runes := []rune(cause); len(runes) > startCauseLimit {
		cause = string(runes[:startCauseLimit]) + "…"
	}
	return cause
}

// RecordStartupSetback records the cause of a failed attempt at one of its
// long startup steps that may pass, while it still wants its start: the
// step is taken again, and a start failure at its Startup Deadline names
// this cause. It reports whether the cause changed.
func (e *AgentExecution) RecordStartupSetback(cause string) bool {
	cause = startCause(cause)
	if cause == "" || e.State != ExecutionStarting || !e.wantsRun() || e.StartupSetback == cause {
		return false
	}
	e.StartupSetback = cause
	return true
}

// FailDefinitely records a definite failure of its start, named by cause,
// while it still wants its run: a start that bound no environment is
// rejected (Creation refusal); one that bound its environment cannot
// complete its Required Startup Steps, so its start fails at once rather
// than at its Startup Deadline. It reports whether it changed.
func (e *AgentExecution) FailDefinitely(cause string) bool {
	cause = startCause(cause)
	if cause == "" || e.State != ExecutionStarting || !e.wantsRun() {
		return false
	}
	if e.Environment == "" {
		e.reject(cause)
		return true
	}
	reason := "The start could not complete: " + cause + "."
	e.Outcome.StartFailure = reason
	e.record(ExecutionStartFailed{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt, Reason: reason})
	return true
}
