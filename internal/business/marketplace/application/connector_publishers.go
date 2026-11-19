package application

import (
	"context"
	"errors"
	"log/slog"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

// unknownPublisher names a publisher Account cannot name.
const unknownPublisher = "Unknown"

// PublisherDirectory names the people and Applications that publish
// Connectors, through Account.
type PublisherDirectory interface {
	// PersonNames returns the display names of the known people among ids.
	PersonNames(ctx context.Context, ids []string) (map[string]string, error)
	// ApplicationName returns an Application's name.
	ApplicationName(ctx context.Context, id string) (string, error)
}

// publisherNames names each person or Application among owners. A name
// Account cannot give, even when Account is unreachable, stays unknown: a
// publisher's name only presents a Connector and never decides a read or a
// Work's start.
func (a *Application) publisherNames(ctx context.Context, owners []query.ConnectorOwner) map[query.ConnectorOwner]string {
	names := map[query.ConnectorOwner]string{}
	var people []string
	applications := map[string]bool{}
	for _, owner := range owners {
		switch owner.Kind {
		case query.OwnerPerson:
			people = append(people, owner.ID)
		case query.OwnerApplication:
			applications[owner.ID] = true
		}
	}
	if a.publishers == nil || (len(people) == 0 && len(applications) == 0) {
		return names
	}
	if len(people) > 0 {
		found, err := a.publishers.PersonNames(ctx, people)
		if err != nil {
			a.logger.WarnContext(ctx, "naming Connector publishers failed", slog.Any("error", err))
		}
		for id, name := range found {
			names[query.ConnectorOwner{Kind: query.OwnerPerson, ID: id}] = name
		}
	}
	for id := range applications {
		name, err := a.publishers.ApplicationName(ctx, id)
		if err != nil && !errors.Is(err, ErrApplicationUnknown) {
			a.logger.WarnContext(ctx, "naming a Connector's publishing Application failed", slog.String("application_id", id), slog.Any("error", err))
		}
		names[query.ConnectorOwner{Kind: query.OwnerApplication, ID: id}] = name
	}
	return names
}

// publisherName is how a Connector names its publisher; the platform needs
// no name.
func publisherName(names map[query.ConnectorOwner]string, owner query.ConnectorOwner) string {
	if owner.Kind == query.OwnerPlatform || owner.Kind == "" {
		return ""
	}
	if name := names[owner]; name != "" {
		return name
	}
	return unknownPublisher
}

// namePublishers names the publisher of each Connector view.
func (a *Application) namePublishers(ctx context.Context, views []Connector) {
	owners := make([]query.ConnectorOwner, 0, len(views))
	for _, view := range views {
		owners = append(owners, view.Publisher.owner)
	}
	names := a.publisherNames(ctx, owners)
	for i := range views {
		views[i].Publisher.DisplayName = publisherName(names, views[i].Publisher.owner)
	}
}
