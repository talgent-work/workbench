package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ApplicationRemoved follows Account's fact that an Application was removed
// for good (Close removed Application's Works): each Work the Application owns
// that is not closed yet gets its own close task, keyed by the Work, so a
// repeated or reordered fact queues nothing twice. Agent keeps no obligation
// for these closes: a lost fact leaves the Works open, undrivable, until
// their idle timeout stops them.
func (r *Recovery) ApplicationRemoved(ctx context.Context, applicationID string) error {
	applicationID = strings.TrimSpace(applicationID)
	if applicationID == "" {
		return domain.ErrInvalidWork
	}
	unclosed := false
	list := func(ctx context.Context, after string, limit int) ([]string, error) {
		works, err := r.app.WorkQueries.ListWorkViews(ctx, query.WorkListFilter{AgentOwner: applicationID, Archived: &unclosed, AfterID: after, Limit: limit})
		ids := make([]string, 0, len(works))
		for _, work := range works {
			ids = append(ids, work.WorkID)
		}
		return ids, err
	}
	closeWork := func(ctx context.Context, workID string) error {
		task, err := agenttask.NewCloseRemovedApplicationWork(workID, applicationID)
		if err != nil {
			return err
		}
		return r.enqueuer.Enqueue(ctx, task, agenttask.RemovedApplicationClose()...)
	}
	return sweep(ctx, list, closeWork, identity)
}

// CloseRemovedApplicationWork closes one Work of a removed Application, as its
// queued task asks.
func (r *Recovery) CloseRemovedApplicationWork(ctx context.Context, applicationID, workID string) error {
	return r.app.Commands.CloseWork.CloseForRemovedApplication(ctx, applicationID, workID)
}
