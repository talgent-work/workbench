package application

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// AdmitExtensionWorkCaller refuses a trusted service's creation of a Work under
// a Work Caller of a kind Agent defines: such a Work is a Standalone Work,
// which only its owner starts through the public Work API.
func (a *Application) AdmitExtensionWorkCaller(caller domain.WorkCaller) error {
	return domain.AdmitExtensionWorkCaller(caller)
}

// AdmitDelegatedControl admits a trusted service acting on its owner's behalf
// only on an Extension Work: the private API's one trusted credential is the
// extension's, which defines every kind Agent does not. An unknown Work is
// left to the command, which reports it.
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
