package application

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// ErrApplicationUnknown reports an Application that Account does not know.
var ErrApplicationUnknown = errors.New("marketplace: application not found")

// OwnedApplication is Account's answer about one Application: who owns it
// and whether it was removed.
type OwnedApplication struct {
	ID          string
	OwnerUserID string
	Removed     bool
}

// ApplicationOwnershipPort asks Account's Application Ownership Query.
type ApplicationOwnershipPort interface {
	GetApplication(ctx context.Context, applicationID string) (OwnedApplication, error)
}

// Caller is the trusted identity a request speaks for: a person, or an
// Application calling as itself.
type Caller struct {
	UserID        string
	ApplicationID string
}

// Owners decides whose installations and Provider Authorizations a request
// reaches: the caller's own, or an Application the calling person owns
// (Application Owner manages).
type Owners struct {
	ownership ApplicationOwnershipPort
}

func NewOwners(ownership ApplicationOwnershipPort) *Owners {
	return &Owners{ownership: ownership}
}

// Resolve returns the owner a request reaches. An Application reaches only
// itself, so it leaves applicationID empty or names itself. A person reaches
// their own, or, naming an Application, that Application while they are its
// Application Owner and it is not removed.
func (o *Owners) Resolve(ctx context.Context, caller Caller, applicationID string) (string, error) {
	applicationID = strings.TrimSpace(applicationID)
	if caller.ApplicationID != "" {
		if applicationID != "" && applicationID != caller.ApplicationID {
			return "", domain.ErrPermissionDenied
		}
		return caller.ApplicationID, nil
	}
	if strings.TrimSpace(caller.UserID) == "" {
		return "", ErrUnauthenticated
	}
	if applicationID == "" {
		return caller.UserID, nil
	}
	owned, err := o.ownership.GetApplication(ctx, applicationID)
	if errors.Is(err, ErrApplicationUnknown) {
		return "", domain.ErrPermissionDenied
	}
	if err != nil {
		return "", err
	}
	if owned.OwnerUserID != caller.UserID || owned.Removed {
		return "", domain.ErrPermissionDenied
	}
	return owned.ID, nil
}
