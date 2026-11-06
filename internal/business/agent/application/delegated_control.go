package application

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// AdmitCallerCreation refuses a trusted caller's creation of a Personal Work,
// which Agent alone starts.
func (a *Application) AdmitCallerCreation(subject domain.WorkSubject, projectID string) error {
	return domain.AdmitCallerCreation(subject, strings.TrimSpace(projectID))
}

// AdmitDelegatedControl refuses a trusted caller acting on its owner's behalf
// on a Personal Work. An unknown Work is left to the command, which reports it.
func (a *Application) AdmitDelegatedControl(ctx context.Context, workID string) error {
	if strings.TrimSpace(workID) == "" {
		return nil
	}
	work, err := a.Works.Get(ctx, strings.TrimSpace(workID))
	if errors.Is(err, domain.ErrWorkNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return work.AdmitDelegatedControl()
}
