package authn

import (
	"context"
	"strings"
)

// AuthenticatedUser is a user Principal: the person a request speaks for, by
// their Account, with the claims the entry verified.
type AuthenticatedUser struct {
	ID           string
	Username     string
	DisplayName  string
	PrimaryEmail string
	AvatarURL    string
	Roles        []string
}

// Agent is an agent Principal: a Work's Agent, by its Work's Agent Account,
// with the Work's fixed facts the entry verified. It carries no person: a
// represented person may be consulted for what the Agent reads, never for
// who acted.
type Agent struct {
	WorkID string
	// ProjectID is empty for a Work without a Project.
	ProjectID string
	// AgentID is the Work's AgentDefinition.
	AgentID string
	Purpose string
	// SubjectKind and SubjectID name the Work's Subject; empty for none.
	SubjectKind string
	SubjectID   string
	// RepresentedUserID is the Account the Agent represents; empty for an
	// Agent that represents no one, such as the PM Coordinator.
	RepresentedUserID string
}

// Principal is the one subject a request speaks for: a user or an agent,
// never both.
type Principal struct {
	user  *AuthenticatedUser
	agent *Agent
}

// UserPrincipal is a request speaking for a person.
func UserPrincipal(user AuthenticatedUser) Principal { return Principal{user: &user} }

// AgentPrincipal is a request speaking for a Work's Agent.
func AgentPrincipal(agent Agent) Principal { return Principal{agent: &agent} }

// User is the person the request speaks for, if it speaks for one.
func (p Principal) User() (AuthenticatedUser, bool) {
	if p.user == nil {
		return AuthenticatedUser{}, false
	}
	return *p.user, true
}

// Agent is the Work's Agent the request speaks for, if it speaks for one.
func (p Principal) Agent() (Agent, bool) {
	if p.agent == nil {
		return Agent{}, false
	}
	return *p.agent, true
}

type principalContextKey struct{}

// WithPrincipal returns a context carrying the request's Principal.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFrom returns the request's Principal; an anonymous procedure's
// request carries none.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

// WithAuthenticatedUser returns a context speaking for user, as a trusted
// caller or a test establishes it; an invalid id leaves ctx unchanged.
func WithAuthenticatedUser(ctx context.Context, user AuthenticatedUser) context.Context {
	id, ok := normalizeUserID(user.ID)
	if !ok {
		return ctx
	}
	user.ID = id
	user.Username = strings.TrimSpace(user.Username)
	user.DisplayName = strings.TrimSpace(user.DisplayName)
	user.PrimaryEmail = strings.TrimSpace(user.PrimaryEmail)
	user.AvatarURL = strings.TrimSpace(user.AvatarURL)
	return WithPrincipal(ctx, UserPrincipal(user))
}

// CurrentUser returns the person the request speaks for; a request speaking
// for an agent has none.
func CurrentUser(ctx context.Context) (AuthenticatedUser, bool) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return AuthenticatedUser{}, false
	}
	user, ok := principal.User()
	if !ok {
		return AuthenticatedUser{}, false
	}
	id, valid := normalizeUserID(user.ID)
	user.ID = id
	return user, valid
}

// CurrentUserID returns the id of the person the request speaks for.
func CurrentUserID(ctx context.Context) (string, bool) {
	user, ok := CurrentUser(ctx)
	return user.ID, ok
}

// CurrentAgent returns the Work's Agent the request speaks for.
func CurrentAgent(ctx context.Context) (Agent, bool) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return Agent{}, false
	}
	return principal.Agent()
}

func normalizeUserID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, "oidc:") || strings.Contains(id, "#") {
		return "", false
	}
	return id, true
}
