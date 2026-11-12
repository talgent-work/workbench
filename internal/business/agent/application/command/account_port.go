package command

import (
	"context"
	"errors"
	"time"
)

// DirectoryUser is one person in Account's User Directory.
type DirectoryUser struct {
	UserID, Username, DisplayName, AvatarURL string
}

// WorkIdentityFacts are a Work's fixed facts, which Account copies into the
// Work's agent Identity Token: its Project, AgentDefinition, purpose,
// Subject, the person its Agent represents, the Application that owns it and
// its Work Labels, each empty when the Work has none.
type WorkIdentityFacts struct {
	WorkID            string
	ProjectID         string
	AgentID           string
	Purpose           string
	SubjectKind       string
	SubjectID         string
	RepresentedPerson string
	OwningApplication string
	// WorkLabels are the Work's Work Labels; nil for none.
	WorkLabels map[string]string
}

// IdentityToken is a Work's agent Identity Token as Account issued it. It is
// a credential: it is delivered, never logged or recorded.
type IdentityToken struct {
	Token     string
	ExpiresAt time.Time
}

// OwnedApplication is an Application as Account's Application Ownership
// Query answers it: its name and its Application Owner, also once removed.
type OwnedApplication struct {
	ID, Name, OwnerUserID string
	Removed               bool
}

// ErrApplicationUnknown means Account knows no such Application.
var ErrApplicationUnknown = errors.New("agent: account knows no such application")

// AccountPort reaches Account: the User Directory, everyone in the
// deployment's one enterprise, which a Work without a Project is shared
// within; the Agent Account Command, which issues a Work's Identity Token;
// and the Application Ownership Query.
type AccountPort interface {
	SearchUsers(ctx context.Context, query string, limit int) ([]DirectoryUser, error)
	GetUsers(ctx context.Context, userIDs []string) ([]DirectoryUser, error)
	// IssueWorkIdentityToken issues the Work's agent Identity Token; Account
	// refuses facts that differ from those of the Work's first request.
	IssueWorkIdentityToken(ctx context.Context, facts WorkIdentityFacts) (IdentityToken, error)
	// GetApplication reads one Application, removed or not; an unknown one
	// is ErrApplicationUnknown.
	GetApplication(ctx context.Context, applicationID string) (OwnedApplication, error)
}

// identityTokenRenewalLead is how long before its Identity Token expires a
// delivery falls due again. Redelivery waits until the recorded expiry has
// passed, so recording the token's own would let the token lapse first; a
// day ahead, a run that outlives its token receives a new one while the old
// still works.
const identityTokenRenewalLead = 24 * time.Hour

// redeliveryDue is the expiry a delivery records: the earlier of what the
// environment declared for the rest of the delivery, zero for none, and one
// renewal lead before the token's own expiry.
func (t IdentityToken) redeliveryDue(declared time.Time) time.Time {
	due := t.ExpiresAt.Add(-identityTokenRenewalLead)
	if declared.IsZero() || due.Before(declared) {
		return due
	}
	return declared
}
