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
)

// ProjectAccessDecision is the Project access decision Observability defines
// and the context owning Project membership implements. It admits a reader to
// a Project's observations, or refuses with ErrAuthorizationPermissionDenied.
type ProjectAccessDecision interface {
	AuthorizeProjectReader(ctx context.Context, readerID, projectID string) error
}

// WorkAccess is Agent's access to a Work without a Project: its owner and
// Work Share holders read it; others are refused with
// ErrAuthorizationPermissionDenied.
type WorkAccess interface {
	AuthorizeWorkReader(ctx context.Context, readerID, workID string) error
}

// WorkScope names one Work's observations. Evidence carries the Work's
// Project, so the empty Project names exactly the Works without one.
type WorkScope struct{ ProjectID, AgentWorkID string }

// actor is the authenticated reader; possessing an identifier grants nothing.
func actor(ctx context.Context) (string, error) {
	if id, _ := authn.CurrentUserID(ctx); id != "" {
		return id, nil
	}
	return "", ErrAuthorizationUnauthenticated
}

func (q *Queries) authorizeProject(ctx context.Context, projectID string) error {
	reader, err := actor(ctx)
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
	reader, err := actor(ctx)
	if err != nil {
		return scope, err
	}
	if q.works == nil {
		return scope, ErrAuthorizationUnavailable
	}
	return scope, q.works.AuthorizeWorkReader(ctx, reader, scope.AgentWorkID)
}
