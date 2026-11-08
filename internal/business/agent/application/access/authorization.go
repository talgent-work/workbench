package access

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var ErrAccessDenied = errors.New("agent: access denied")

// WorkAuthorizer checks the caller's authority for one conversation.
// Authentication and reference selection remain at the inbound boundary.
type WorkAuthorizer interface {
	AuthorizeWork(context.Context, string, string) error
}

// ownerAuthorization applies Admit control requester to the Owners of the
// Work's definition. Callers delegate an Owner only after authorizing their
// outer request; Agent adds no Project rules.
type ownerAuthorization struct {
	owners func(ctx context.Context, workID string) ([]string, bool, error)
}

// NewWorkAuthorizer authorizes a requester of a Work from the Owners of its
// definition that owners reads without loading the Work, false for an
// unknown Work; the Work query repository's WorkOwners reads them.
func NewWorkAuthorizer(owners func(ctx context.Context, workID string) ([]string, bool, error)) WorkAuthorizer {
	return ownerAuthorization{owners: owners}
}

func (a ownerAuthorization) AuthorizeWork(ctx context.Context, subject, id string) error {
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(id) == "" {
		return ErrAccessDenied
	}
	owners, found, err := a.owners(ctx, id)
	if err != nil {
		return err
	}
	if !found || domain.AdmitAgentOwner(owners, subject) != nil {
		return ErrAccessDenied
	}
	return nil
}
