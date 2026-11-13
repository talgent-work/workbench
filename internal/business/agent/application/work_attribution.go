package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// MaxAttributedWorks bounds the Works one attribution request names.
const MaxAttributedWorks = 500

// ListWorkAttributions answers, for a trusted context keeping no copy of
// Work Labels, which of the named Works the Application owns and the labels
// each carries (AgentWork Attribution Query). A Work the Application does not
// own, or an unknown one, is left out; an Application without a definition
// owns none.
func (a *Application) ListWorkAttributions(ctx context.Context, applicationID string, workIDs []string) ([]query.WorkAttribution, error) {
	applicationID = strings.TrimSpace(applicationID)
	if applicationID == "" {
		return nil, fmt.Errorf("%w: application is required", domain.ErrInvalidWork)
	}
	if len(workIDs) > MaxAttributedWorks {
		return nil, fmt.Errorf("%w: at most %d Works are attributed at once", domain.ErrInvalidWork, MaxAttributedWorks)
	}
	definition, found, err := a.FindApplicationAgent(ctx, applicationID)
	if err != nil || !found {
		return nil, err
	}
	named := make([]string, 0, len(workIDs))
	for _, id := range workIDs {
		if id = strings.TrimSpace(id); id != "" {
			named = append(named, id)
		}
	}
	return a.WorkQueries.ListWorkAttributions(ctx, definition, named)
}
