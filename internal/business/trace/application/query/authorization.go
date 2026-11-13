package query

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/pkg/authn"
)

var (
	ErrAuthorizationUnauthenticated  = errors.New("authorization: actor is not authenticated")
	ErrAuthorizationPermissionDenied = errors.New("authorization: permission denied")
	ErrAuthorizationUnavailable      = errors.New("authorization: service unavailable")
	// ErrApplicationUnknown is Account's answer for an Application it never
	// registered.
	ErrApplicationUnknown = errors.New("authorization: unknown application")
)

// WorkCallerAccessPort is the access decision of the extension that defines
// a Work Caller's kind, which Observability defines and each such extension
// implements; the Work Caller's kind selects which one is asked. It admits a
// person to the observations of a Work Caller's Works, or refuses with
// ErrAuthorizationPermissionDenied, also for a kind no extension decides in
// this deployment.
type WorkCallerAccessPort interface {
	AuthorizeWorkCallerReader(ctx context.Context, readerID string, caller WorkCaller) error
}

// WorkAttribution is one of an Application's Works with the Work Labels it
// carries, which Observability reads from Agent and never keeps.
type WorkAttribution struct {
	WorkID string
	Labels map[string]string
}

// AgentPort is Agent's authority over Standalone Works: whether a reader, a
// person or an Application, may read one (Work Access Query), and
// which named Works an Application owns with their Work Labels (AgentWork
// Attribution Query).
type AgentPort interface {
	// AuthorizeWorkReader admits the Work's owner and Work Share holders and
	// refuses others with ErrAuthorizationPermissionDenied.
	AuthorizeWorkReader(ctx context.Context, readerID, workID string) error
	// ListWorkAttributions answers for those of the named Works the
	// Application owns, at most MaxAttributedWorks at once.
	ListWorkAttributions(ctx context.Context, applicationID string, workIDs []string) ([]WorkAttribution, error)
}

// MaxAttributedWorks bounds the Works one AgentPort attribution names.
const MaxAttributedWorks = 500

// OwnedApplication is an Application with its Application Owner, as Account
// answers it, removed or not.
type OwnedApplication struct {
	ID, OwnerUserID string
	Removed         bool
}

// AccountPort is Account's Application Ownership Query.
type AccountPort interface {
	// GetApplication answers ErrApplicationUnknown for an Application
	// Account never registered.
	GetApplication(ctx context.Context, applicationID string) (OwnedApplication, error)
}

// WorkScope names one Work's observations, or, with no Work, a Work
// Caller's. A Work Caller of an extension-defined kind scopes an Extension
// Work; a Standalone Work is named without one.
type WorkScope struct {
	WorkCaller  WorkCaller
	AgentWorkID string
}

// errWorkNotObserved marks a Standalone Work its admitted reader reads before
// it has measurements: there is nothing to disclose, so a read answers with
// nothing.
var errWorkNotObserved = errors.New("work has no observations")

// reader is the authenticated person or Application a read is for, never
// both; possessing an identifier grants nothing.
type reader struct{ userID, applicationID string }

// id is the reader as Agent's Work access knows it.
func (r reader) id() string {
	if r.applicationID != "" {
		return r.applicationID
	}
	return r.userID
}

func readerOf(ctx context.Context) (reader, error) {
	if application, ok := authn.CurrentApplication(ctx); ok && strings.TrimSpace(application.ID) != "" {
		return reader{applicationID: strings.TrimSpace(application.ID)}, nil
	}
	if id, _ := authn.CurrentUserID(ctx); id != "" {
		return reader{userID: id}, nil
	}
	return reader{}, ErrAuthorizationUnauthenticated
}

// person is the authenticated person; an Application reads nothing a person
// reads as themselves.
func person(ctx context.Context) (string, error) {
	r, err := readerOf(ctx)
	if err != nil {
		return "", err
	}
	if r.userID == "" {
		return "", ErrAuthorizationPermissionDenied
	}
	return r.userID, nil
}

// authorizeWorkCaller admits a person to the observations of an Extension
// Work Caller's Works under the access decision of the extension defining its
// kind. An Application reads no Extension Work.
func (q *Queries) authorizeWorkCaller(ctx context.Context, caller WorkCaller) error {
	reader, err := person(ctx)
	if err != nil {
		return err
	}
	return q.workCallers.AuthorizeWorkCallerReader(ctx, reader, caller)
}

// authorizeScope admits the reader to the scope and resolves it to the Work
// Caller evidence carries. workRequired also requires the Work under a Work
// Caller.
//
// Without a Work Caller the scope is one Standalone Work, and Agent's Work
// access admits the reader to that Work itself. With one, the extension's
// access decision admits the reader to the Work Caller, not to any Work, so a
// Work named under it must be proven one of its Works by the Work Caller its
// measurements carry.
//
// Unobserved Works follow from what the reader was admitted to. A Work the
// reader was admitted to itself but that has no measurements yet answers with
// nothing (errWorkNotObserved): its existence is no news to that reader. A
// Work not proven under the Work Caller it is named in is ErrWorkNotFound,
// whether it has no measurements or carries another Work Caller, and so is an
// Extension Work named as a Standalone one; the refusals look alike, so no
// reader probes which Works exist under Work Callers they cannot read.
func (q *Queries) authorizeScope(ctx context.Context, scope WorkScope, workRequired bool) (WorkScope, error) {
	scope.AgentWorkID = strings.TrimSpace(scope.AgentWorkID)
	if workRequired || !scope.WorkCaller.Present() {
		if _, err := required(scope.AgentWorkID, "agent_work_id"); err != nil {
			return scope, err
		}
	}
	if scope.WorkCaller.Present() {
		return q.authorizeExtensionScope(ctx, scope)
	}
	reader, err := readerOf(ctx)
	if err != nil {
		return scope, err
	}
	if q.agent == nil {
		return scope, ErrAuthorizationUnavailable
	}
	if err := q.agent.AuthorizeWorkReader(ctx, reader.id(), scope.AgentWorkID); err != nil {
		return scope, err
	}
	caller, observed, err := q.measurements.GetWorkCaller(ctx, scope.AgentWorkID)
	switch {
	case err != nil:
		return scope, err
	case !observed:
		return scope, errWorkNotObserved
	case !caller.Standalone():
		return scope, ErrWorkNotFound
	}
	scope.WorkCaller = caller
	return scope, nil
}

func (q *Queries) authorizeExtensionScope(ctx context.Context, scope WorkScope) (WorkScope, error) {
	caller, err := extensionCaller(scope.WorkCaller)
	if err != nil {
		return scope, err
	}
	if err := q.authorizeWorkCaller(ctx, caller); err != nil {
		return scope, err
	}
	scope.WorkCaller = caller
	if scope.AgentWorkID == "" {
		return scope, nil
	}
	actual, observed, err := q.measurements.GetWorkCaller(ctx, scope.AgentWorkID)
	if err != nil {
		return scope, err
	}
	if !observed || actual != caller {
		return scope, ErrWorkNotFound
	}
	return scope, nil
}

// authorizeApplication resolves the Application a read names: the
// Application itself, which may name only itself, or an Application the
// person owns, also after its removal (Application usage access).
func (q *Queries) authorizeApplication(ctx context.Context, requested string) (string, error) {
	reader, err := readerOf(ctx)
	if err != nil {
		return "", err
	}
	requested = strings.TrimSpace(requested)
	if reader.applicationID != "" {
		if requested != "" && requested != reader.applicationID {
			return "", ErrAuthorizationPermissionDenied
		}
		return reader.applicationID, nil
	}
	if requested, err = required(requested, "application_id"); err != nil {
		return "", err
	}
	if q.accounts == nil {
		return "", ErrAuthorizationUnavailable
	}
	owned, err := q.accounts.GetApplication(ctx, requested)
	if errors.Is(err, ErrApplicationUnknown) {
		return "", ErrAuthorizationPermissionDenied
	}
	if err != nil {
		return "", errors.Join(ErrAuthorizationUnavailable, err)
	}
	if owned.OwnerUserID == "" || owned.OwnerUserID != reader.userID {
		return "", ErrAuthorizationPermissionDenied
	}
	return requested, nil
}
