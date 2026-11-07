package command

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// AgentWorkRepository is the AgentWork repository as use cases change Works:
// the Domain collection, together with the saves that record a caller's
// creation or start request with the Work it changes. Each such save commits
// the request record atomically with the Work, through the same persistence
// and concurrency path as Save. A start request identity serves only
// control-request idempotency; it never distinguishes processes.
type AgentWorkRepository interface {
	domain.AgentWorkRepository
	query.StartAdmissionReader
	// RegisterWork persists one new Work against the definition revision used
	// for its creation, together with its creation request and, when
	// supplied, its initial input, so a registered Work never loses its
	// original startup input. A competing registration or changed definition
	// returns domain.ErrConcurrentModification; the use case reloads and lets
	// AgentDefinition locate it.
	RegisterWork(ctx context.Context, work *domain.AgentWork, registration WorkRegistration) error
	// CommitStart persists the admitted Work together with its start request.
	// Explicit Restart also reopens a retained rejected initial preparation
	// before task execution, preserving its original input and request
	// identity. The commit fails with ErrStartRequestCanceled when the request
	// was canceled before admission. Inputs admitted to an earlier process run
	// and never settled are settled with the new start: they are dropped
	// rather than replayed into the new process.
	CommitStart(ctx context.Context, work *domain.AgentWork, start query.AdmittedStart, requestID string) error
	// CancelStart prevents a start request that was not admitted, under the
	// same serialization as CommitStart. It returns false when the request was
	// already admitted for this Work.
	CancelStart(ctx context.Context, workID, requestID string) (bool, error)
	// CancelWorkRegistration serializes cancellation with initial Work
	// registration. Empty means this request cannot create a Work; otherwise
	// it returns the already admitted Work without changing its lifecycle.
	CancelWorkRegistration(ctx context.Context, request CancelWorkRegistration) (string, error)
}

// CancelWorkRegistration carries the original creation correlation supplied by
// an authenticated business caller, before it knows the resulting Work ID.
// The caller owns authorization of this scope, as with CreateWork.
type CancelWorkRegistration struct {
	RequestID, SubjectID, AgentID, WorkspaceReference string
	Subject                                           domain.WorkSubject
	ProjectID                                         string
}
