package query

import (
	"context"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkOrigin exposes retained attribution without imposing execution admission.
type WorkOrigin struct {
	WorkID, AgentID, Purpose string
	WorkCaller               domain.WorkCaller
	WorkKey                  domain.WorkKey
	FileHome                 domain.FileHome
	// StartedBy is who started the Work, a fact; its Agent Owners are its
	// definition's Owners. StartedAt is when the Work was created.
	StartedBy string
	StartedAt time.Time
}

type GetWorkOriginHandler struct{ repository WorkQueryRepository }

func NewGetWorkOriginHandler(repository WorkQueryRepository) *GetWorkOriginHandler {
	return &GetWorkOriginHandler{repository: repository}
}

func (h *GetWorkOriginHandler) Handle(ctx context.Context, sessionID string) (WorkOrigin, error) {
	if strings.TrimSpace(sessionID) == "" {
		return WorkOrigin{}, domain.ErrInvalidWork
	}
	return h.repository.GetWorkOrigin(ctx, sessionID)
}
