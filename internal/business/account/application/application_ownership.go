package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// ApplicationOwnership is an Application with its Application Owner, as the
// Application Ownership Query answers other contexts.
type ApplicationOwnership struct {
	ID    string
	Name  string
	Owner string
	// Removed is true once the Application was removed for good; it is still
	// answered, so its owner keeps reading what it produced (Ownership
	// answered).
	Removed bool
}

// ApplicationOwnership reads one Application, removed or not; an unknown one
// answers ErrApplicationNotFound.
func (a *Application) ApplicationOwnership(ctx context.Context, id string) (ApplicationOwnership, error) {
	applicationID, err := domain.ParseApplicationID(id)
	if err != nil {
		return ApplicationOwnership{}, err
	}
	found, err := a.applications.Get(ctx, applicationID)
	if err != nil {
		return ApplicationOwnership{}, err
	}
	return ApplicationOwnership{ID: string(found.ID), Name: found.Name, Owner: string(found.ApplicationOwner), Removed: found.State == domain.ApplicationRemoved}, nil
}
