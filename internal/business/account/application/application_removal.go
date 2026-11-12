package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// AccountPublisherPort publishes Account's Published Fact Contracts.
type AccountPublisherPort interface {
	// PublishApplicationRemoved publishes the Application Removed Published
	// Fact, keyed by the Application.
	PublishApplicationRemoved(ctx context.Context, removed domain.ApplicationRemovedEvent) error
}

// publishTimeout bounds the publication that follows a committed removal, so
// an unreachable broker never holds the owner's request.
const publishTimeout = 5 * time.Second

// RemoveApplication names an Application its owner removes.
type RemoveApplication struct {
	Owner         string
	ApplicationID string
}

// RemoveApplication removes the Application for good: every Client Secret
// ends in the same commit, so the grant refuses it at once, and tokens it
// already obtained stop at their short expiry. Application Removed is then
// published best effort: Agent closes the Application's Works on it, and a
// publication that fails is logged and leaves them to their idle timeout.
func (a *Application) RemoveApplication(ctx context.Context, cmd RemoveApplication) error {
	var events []domain.Event
	// A concurrent grant that records a use commits first; the repeat reloads.
	err := retryOnceOnConflict(func() error {
		owner, found, err := a.applicationFor(ctx, cmd.Owner, cmd.ApplicationID)
		if err != nil {
			return err
		}
		if err := found.Remove(owner, a.now()); err != nil {
			return err
		}
		if err := a.applications.Save(ctx, found); err != nil {
			return err
		}
		events = found.Events.Drain()
		return nil
	})
	if err != nil {
		return err
	}
	for _, event := range events {
		removed, ok := event.(domain.ApplicationRemovedEvent)
		if !ok {
			continue
		}
		publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
		err := a.publisher.PublishApplicationRemoved(publishCtx, removed)
		cancel()
		if err != nil {
			// The removal stands; only Agent's closing of its Works is lost.
			a.logger.WarnContext(ctx, "account: application removed not published; its Works stop at their idle timeout",
				slog.String("application_id", string(removed.ApplicationID)), slog.Any("error", err))
		}
	}
	return nil
}

// noPublisher publishes nothing, for a deployment without a message bus.
type noPublisher struct{}

func (noPublisher) PublishApplicationRemoved(context.Context, domain.ApplicationRemovedEvent) error {
	return nil
}
