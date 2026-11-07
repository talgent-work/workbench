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
// and concurrency path as Save. The start-request record is the single record
// of caller request identities for creation, Resume and Restart; it serves
// only request idempotency and never distinguishes processes.
type AgentWorkRepository interface {
	domain.AgentWorkRepository
	query.StartRequestReader
	// RegisterWork persists one new Work, with its initial task input,
	// against the definition revision used for its creation, together with
	// its creation request record and, when supplied, its first start's
	// preparation. A competing registration or changed definition returns
	// domain.ErrConcurrentModification; the use case reloads and lets the
	// request record or AgentDefinition locate the Work. A creation request
	// already cancelled fails with ErrStartRequestCanceled, and one recorded
	// with other content or for another Work with ErrStartRequestConflict.
	RegisterWork(ctx context.Context, work *domain.AgentWork, registration WorkRegistration) error
	// CommitStart persists the admitted Work together with its start request
	// record. Explicit Restart also reopens a retained rejected initial
	// preparation before task execution, preserving its original request
	// identity. The commit fails with ErrStartRequestCanceled when the request
	// was cancelled before admission, and with ErrStartRequestConflict when
	// its identity is recorded for other content or another Work.
	CommitStart(ctx context.Context, work *domain.AgentWork, request query.StartRequest) error
	// CancelStart prevents a start request that was not admitted, under the
	// same serialization as CommitStart. It returns false when the request was
	// already admitted for this Work.
	CancelStart(ctx context.Context, workID, requestID string) (bool, error)
	// RecordCreation records the outcome of a creation request identity that
	// registered no Work of its own, unless the identity is already recorded,
	// under the same serialization as RegisterWork, and returns the retained
	// record: the first writer keeps it. The outcome is a reused Work, a
	// definitive refusal settled as cancelled with the request's fingerprint,
	// or a caller's cancellation, before Agent may have received the request,
	// without one.
	RecordCreation(ctx context.Context, request query.StartRequest) (query.StartRequest, error)
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
