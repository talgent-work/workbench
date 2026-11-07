package application

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (a *Application) CancelWorkRegistration(ctx context.Context, request command.CancelWorkRegistration) (string, error) {
	if uuid.Validate(request.RequestID) != nil {
		return "", query.ErrInvalidStartRequest
	}
	if strings.TrimSpace(request.SubjectID) == "" || len(request.SubjectID) > 255 {
		return "", access.ErrAccessDenied
	}
	if uuid.Validate(request.AgentID) != nil || strings.TrimSpace(request.ProjectID) == "" {
		return "", domain.ErrInvalidWork
	}
	if err := request.Subject.Validate(); err != nil {
		return "", err
	}
	id, err := a.Works.CancelWorkRegistration(ctx, request)
	if err != nil || id == "" {
		return "", err
	}
	if err := a.WorkPrincipals.AuthorizeWork(ctx, request.SubjectID, id); err != nil {
		return "", err
	}
	work, err := a.Works.Get(ctx, id)
	if err != nil {
		return "", err
	}
	// A Work on a Managed Computer keeps none of the Session Workspace its
	// caller named, so only a kept one is compared.
	if work.AgentID != request.AgentID || work.WorkspaceReference != "" && work.WorkspaceReference != request.WorkspaceReference || work.Subject != request.Subject || work.ProjectID != request.ProjectID {
		return "", command.ErrStartRequestConflict
	}
	return id, nil
}
