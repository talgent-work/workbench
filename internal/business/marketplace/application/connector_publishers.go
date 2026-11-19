package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

// ErrPublishersUnavailable reports a deployment without the directory that
// names Connector publishers.
var ErrPublishersUnavailable = errors.New("marketplace: Connector publishers cannot be named")

// PublisherDirectory names the people and Applications that publish
// Connectors, through Account.
type PublisherDirectory interface {
	// PersonNames returns the display names of the known people among ids.
	PersonNames(ctx context.Context, ids []string) (map[string]string, error)
	// ApplicationName returns an Application's name.
	ApplicationName(ctx context.Context, id string) (string, error)
}

// namePublishers replaces each person or Application publisher's identity
// with its display name; one Account cannot name falls back to "Unknown".
func (a *Application) namePublishers(ctx context.Context, views []Connector) error {
	var people []string
	applications := map[string]string{}
	for _, view := range views {
		switch view.Publisher.Kind {
		case query.OwnerPerson:
			people = append(people, view.Publisher.DisplayName)
		case query.OwnerApplication:
			applications[view.Publisher.DisplayName] = ""
		}
	}
	if len(people) == 0 && len(applications) == 0 {
		return nil
	}
	if a.publishers == nil {
		return ErrPublishersUnavailable
	}
	names := map[string]string{}
	if len(people) > 0 {
		found, err := a.publishers.PersonNames(ctx, people)
		if err != nil {
			return err
		}
		names = found
	}
	for id := range applications {
		name, err := a.publishers.ApplicationName(ctx, id)
		if err != nil && !errors.Is(err, ErrApplicationUnknown) {
			return err
		}
		applications[id] = name
	}
	for i := range views {
		publisher := &views[i].Publisher
		var name string
		switch publisher.Kind {
		case query.OwnerPerson:
			name = names[publisher.DisplayName]
		case query.OwnerApplication:
			name = applications[publisher.DisplayName]
		default:
			continue
		}
		if name == "" {
			name = "Unknown"
		}
		publisher.DisplayName = name
	}
	return nil
}
