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

// WorkAccessReader reads the facts Work access is decided from without
// loading the Work.
type WorkAccessReader interface {
	// WorkOwners reads the Owners of the Work's definition; false for an
	// unknown Work.
	WorkOwners(ctx context.Context, workID string) ([]string, bool, error)
}

// ownerAuthorization applies Admit control requester to the Owners of the
// Work's definition. Callers delegate an Owner only after authorizing their
// outer request; Agent adds no Project rules.
type ownerAuthorization struct{ reader WorkAccessReader }

// NewWorkAuthorizer authorizes a requester of a Work from the facts reader
// reads.
func NewWorkAuthorizer(reader WorkAccessReader) WorkAuthorizer {
	return ownerAuthorization{reader: reader}
}

func (a ownerAuthorization) AuthorizeWork(ctx context.Context, subject, id string) error {
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(id) == "" {
		return ErrAccessDenied
	}
	owners, found, err := a.reader.WorkOwners(ctx, id)
	if err != nil {
		return err
	}
	if !found || domain.AdmitAgentOwner(owners, subject) != nil {
		return ErrAccessDenied
	}
	return nil
}
