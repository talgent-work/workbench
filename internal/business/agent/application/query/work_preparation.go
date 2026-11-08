package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
)

// WorkPreparation is a Work's first start as derived from its facts: its
// creation request, initial task input not handed off yet, repository
// selection with its checkout plan, and whether every selected repository
// has a checkout outcome (InputPrepared). Business callers consume it
// without owning preparation or buffering.
type WorkPreparation struct {
	ThinkingLevel                            string
	WorkID, RequestID, SubjectID, WorkBranch string
	InputID, Input                           string
	Submitted, InputPrepared, InputBuffered  bool
	Checkouts                                []PreparedCheckout
}

type PreparedCheckout struct {
	Provider, RepoID, RepoName, CloneURL, WebURL, DefaultBranch string
	BaseBranch, WorkBranch, TargetPath, AccessID                string
}

type GetWorkPreparationHandler struct {
	reader WorkQueryRepository
	access access.WorkAuthorizer
}

func NewGetWorkPreparationHandler(reader WorkQueryRepository, authorization access.WorkAuthorizer) *GetWorkPreparationHandler {
	return &GetWorkPreparationHandler{reader: reader, access: authorization}
}
func (h *GetWorkPreparationHandler) Handle(ctx context.Context, input GetWork) (WorkPreparation, bool, error) {
	if err := h.access.AuthorizeWork(ctx, input.SubjectID, input.WorkID); err != nil {
		return WorkPreparation{}, false, err
	}
	return h.reader.GetWorkPreparation(ctx, input.WorkID)
}

type ListSubmittedPreparationsHandler struct{ reader WorkQueryRepository }

func NewListSubmittedPreparationsHandler(reader WorkQueryRepository) *ListSubmittedPreparationsHandler {
	return &ListSubmittedPreparationsHandler{reader: reader}
}
func (h *ListSubmittedPreparationsHandler) Handle(ctx context.Context, after string) ([]WorkPreparation, error) {
	return h.reader.ListSubmittedPreparations(ctx, after, 100)
}
