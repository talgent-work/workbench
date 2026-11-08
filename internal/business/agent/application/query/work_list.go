package query

import (
	"context"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkSummary is one Work's collaboration facts for a caller's list. The caller
// composes it with its own facts; Agent does not interpret the Subject.
type WorkSummary struct {
	Subject       domain.WorkSubject
	ProjectID     string
	WorkID, State string
	// AgentOwner is the Work's definition's Owner; StartedBy who started it.
	AgentOwner, StartedBy string
	StateReason           string
	ArchivePending        bool
	// Waiting reports a Work whose user owes the answer to a question, even
	// after it stopped.
	Waiting bool
	// DisconnectedSince is the origin of its current Disconnection; zero
	// while observed.
	DisconnectedSince    time.Time
	CreatedAt, UpdatedAt time.Time
}

// WorkListFilter selects Works by Project, the definition's opaque purpose,
// the Agent Owner (an Owner of the Work's definition) or identities. Every supplied condition applies.
type WorkListFilter struct {
	ProjectID                    string
	WorkIDs                      []string
	Purpose, AgentOwner, AfterID string
	// ShareReader selects Works sharing their message stream with that reader.
	ShareReader string
	Limit       int
	Recent      bool
	Archived    *bool
}

type ListWorks struct{ Filter WorkListFilter }

type ListWorksHandler struct{ reader WorkQueryRepository }

func NewListWorksHandler(reader WorkQueryRepository) *ListWorksHandler {
	return &ListWorksHandler{reader: reader}
}

// MaxListedWorks is the largest page one list returns.
const MaxListedWorks = 500

// Handle lists Works for a trusted caller, which authorizes its own scope.
// An unbounded list of every Work is never served.
func (h *ListWorksHandler) Handle(ctx context.Context, input ListWorks) ([]WorkSummary, error) {
	filter := input.Filter
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	if filter.ProjectID == "" && len(filter.WorkIDs) == 0 && strings.TrimSpace(filter.AgentOwner) == "" {
		return nil, domain.ErrInvalidWork
	}
	if len(filter.WorkIDs) > MaxListedWorks {
		return nil, domain.ErrInvalidWork
	}
	if filter.Limit <= 0 || filter.Limit > MaxListedWorks {
		filter.Limit = MaxListedWorks
	}
	return h.reader.ListWorkViews(ctx, filter)
}
