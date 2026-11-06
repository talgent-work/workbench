package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var (
	ErrInitialExecutionPending  = errors.New("agent: initial input preparation is pending")
	ErrInitialExecutionConflict = errors.New("agent: conflicting initial execution request")
	ErrInitialExecutionNotFound = errors.New("agent: initial execution request not found")
	ErrPreparationRejected      = errors.New("agent: preparation was definitively rejected")
	ErrInitialExecutionRejected = errors.New("agent: initial execution request was rejected")
)

type RepositorySelection struct {
	RepositoryID string
	BaseBranch   string
}

// RepositoryCheckout is Agent's retained workspace plan for one selected
// repository. Marketplace retains personal authorization and credential ownership.
type RepositoryCheckout struct {
	Provider, RepoID, RepoName, CloneURL, WebURL, DefaultBranch string
	BaseBranch, WorkBranch, TargetPath                          string
	AccessID                                                    string `json:"CredentialGrantID"`
}

// RepositoryCheckoutResult is durable preparation feedback. It does not change
// the retained selection or schedule automatic retry after input delivery.
type RepositoryCheckoutResult struct {
	RepositoryID, TargetPath string
	Prepared                 bool
	Reason                   string
}

type PrepareWorkExecution struct {
	// Initial Harness setting, independent of the Session's fixed capabilities.
	ThinkingLevel string
	// Set by Agent's creation workflow, never supplied by the public input API.
	CreationRequestID string
	// WorkspaceReference is the Session Workspace reference the creating
	// caller named, part of its creation correlation; filled by Agent before
	// retaining the request, even for a Work that keeps no Session Workspace.
	WorkspaceReference string
	SubjectID          string
	// Keep the retained request key stable across the AgentWork rename.
	WorkID    string `json:"SessionID"`
	RequestID string
	// Branch the caller names for repository checkouts.
	WorkBranch string
	Input      ExecutionInput
	Selections []RepositorySelection
}

// InitialExecutionRequest is application recovery data. It does not own a
// conversation lifecycle or become another execution Aggregate.
type InitialExecutionRequest struct {
	PrepareWorkExecution
	// PreparationCanceled fences late planning and credential supply once the
	// Work closes; a retry never reopens canceled preparation.
	PreparationCanceled bool
	FailureReason       string
	RecoveryPending     bool
	// StartFailed records that definitive preparation failed after the start
	// was admitted, so the failure must be applied to that start.
	StartFailed bool
	// RejectionApplied records that the failed start was ended, so a later
	// Resume is never ended for it.
	RejectionApplied bool
	Checkouts        []RepositoryCheckout
	CheckoutResults  []RepositoryCheckoutResult
	// InputPrepared records that every planned repository ended checked out or
	// not checked out, as reported by the Work's own preparation process.
	Prepared, Submitted, InputPrepared bool
	// InputBuffered records that the initial task input was accepted into the
	// Work's input buffer, ahead of any later input and before the Harness
	// starts. It keeps the key of the delivery it replaced, which
	// it implies, so retained requests read alike.
	InputBuffered              bool `json:"InputDelivered"`
	Rejected, CleanupConfirmed bool
	Version                    int
}

type InitialExecutionReader interface {
	Get(context.Context, string) (InitialExecutionRequest, error)
}

type InitialExecutionRequests interface {
	InitialExecutionReader
	Save(context.Context, InitialExecutionRequest) error
}

// FirstStartRecovered reports that the first start owes nothing more to its
// recovery: its initial task input is buffered and its repository checkout
// ended. The Work's own lifecycle carries the rest of the start.
func (r InitialExecutionRequest) FirstStartRecovered() bool {
	return r.InputBuffered && r.InputPrepared
}

// RetryPreparation reopens only a rejected first start's preparation.
// The caller commits this with explicit Restart admission; background recovery
// never turns a rejected prerequisite into another execution admission.
func (r *InitialExecutionRequest) RetryPreparation() (bool, error) {
	if !r.Rejected || !r.StartFailed {
		return false, nil
	}
	if r.PreparationCanceled || r.CleanupConfirmed {
		return false, ErrInitialExecutionRejected
	}
	r.Rejected, r.StartFailed, r.RecoveryPending, r.RejectionApplied = false, false, false, false
	r.FailureReason = ""
	return true, nil
}

// releaseInitialPreparation fences a closed Work's retained preparation, so a
// late retry can neither plan nor receive repository access again. The Work
// decides whether release is allowed (Admit preparation release).
func releaseInitialPreparation(ctx context.Context, requests InitialExecutionRequests, work *domain.AgentWork, request InitialExecutionRequest) error {
	if request.CleanupConfirmed {
		return nil
	}
	if err := work.AuthorizePreparationRelease(); err != nil {
		return err
	}
	request.PreparationCanceled, request.CleanupConfirmed = true, true
	err := requests.Save(ctx, request)
	if errors.Is(err, domain.ErrConcurrentModification) {
		latest, readErr := requests.Get(ctx, request.WorkID)
		if readErr == nil && latest.RequestID == request.RequestID && latest.CleanupConfirmed {
			return nil
		}
	}
	return err
}
