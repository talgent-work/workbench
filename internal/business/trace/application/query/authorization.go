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

// ProjectAccessPort is the Project access decision Observability defines and
// the context owning Project membership implements. It admits a person to a
// Project's observations, or refuses with ErrAuthorizationPermissionDenied.
type ProjectAccessPort interface {
	AuthorizeProjectReader(ctx context.Context, readerID, projectID string) error
}

// WorkAttribution is one of an Application's Works with the Work Labels it
// carries, which Observability reads from Agent and never keeps.
type WorkAttribution struct {
	WorkID string
	Labels map[string]string
}

// AgentPort is Agent's authority over Works without a Project: whether a
// reader, a person or an Application, may read one (Work Access Query), and
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

// WorkScope names one Work's observations. Evidence carries the Work's
// Project, so the empty Project names exactly the Works without one.
type WorkScope struct{ ProjectID, AgentWorkID string }

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

// authorizeProject admits a person to a Project's observations under the
// Project access decision. An Application's Works have no Project, so the
// decision never admits one.
func (q *Queries) authorizeProject(ctx context.Context, projectID string) error {
	reader, err := person(ctx)
	if err != nil {
		return err
	}
	if q.projects == nil {
		return ErrAuthorizationUnavailable
	}
	return q.projects.AuthorizeProjectReader(ctx, reader, projectID)
}

// authorizeScope admits the reader to a Project's observations under the
// Project access decision, or, without a Project, to one Work's under Agent's
// Work access; a scope naming neither is invalid. workRequired also requires
// the Work within a Project. It returns the normalized scope.
func (q *Queries) authorizeScope(ctx context.Context, scope WorkScope, workRequired bool) (WorkScope, error) {
	scope.ProjectID, scope.AgentWorkID = strings.TrimSpace(scope.ProjectID), strings.TrimSpace(scope.AgentWorkID)
	if workRequired || scope.ProjectID == "" {
		if _, err := required(scope.AgentWorkID, "agent_work_id"); err != nil {
			return scope, err
		}
	}
	if scope.ProjectID != "" {
		return scope, q.authorizeProject(ctx, scope.ProjectID)
	}
	reader, err := readerOf(ctx)
	if err != nil {
		return scope, err
	}
	if q.agent == nil {
		return scope, ErrAuthorizationUnavailable
	}
	return scope, q.agent.AuthorizeWorkReader(ctx, reader.id(), scope.AgentWorkID)
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
