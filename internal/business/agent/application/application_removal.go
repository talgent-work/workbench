package application

import (
	"context"
	"errors"
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
	// First the Application stops acting as its Agent's Owner, so no stream,
	// read or control it still holds is admitted again; then its Works close.
	found, err := r.recordApplicationRemoved(ctx, applicationID)
	if err != nil || !found {
		return err
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

// recordApplicationRemoved records the removal on the Application's Agent
// (Removed Application admits nothing), reporting whether the Application has
// one; an Application that never needed its Agent has no Work either.
func (r *Recovery) recordApplicationRemoved(ctx context.Context, applicationID string) (bool, error) {
	found, exists, err := r.app.FindApplicationAgent(ctx, applicationID)
	if err != nil || !exists {
		return false, err
	}
	// A concurrent change of the definition commits first; a bounded number
	// of repeats reload it, and the consumer redelivers the fact after that.
	for range 8 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		definition, err := r.app.Definitions.Get(ctx, found.AgentID)
		if err != nil {
			return false, err
		}
		changed, err := definition.RecordApplicationRemoved()
		if err != nil || !changed {
			return true, err
		}
		if err = r.app.Definitions.Save(ctx, definition); errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		return true, err
	}
	return false, domain.ErrConcurrentModification
}

// CloseRemovedApplicationWork closes one Work of a removed Application, as its
// queued task asks.
func (r *Recovery) CloseRemovedApplicationWork(ctx context.Context, applicationID, workID string) error {
	_, definition, err := r.app.WorkWithDefinition(ctx, workID)
	if err != nil {
		return err
	}
	return r.app.Commands.CloseWork.CloseForRemovedApplication(ctx, definition, applicationID, workID)
}
