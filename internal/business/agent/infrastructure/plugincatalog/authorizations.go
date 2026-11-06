package plugincatalog

import (
	"context"
	"time"

	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var _ command.PersonalConnectionsPort = (*Client)(nil)

func (c *Client) ListPersonalAuthorizations(ctx context.Context, owner string) ([]domain.PersonalAuthorization, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := delegated(ctx, owner, &marketplacepublicv1.ListPersonalAuthorizationsRequest{})
	if err != nil {
		return nil, err
	}
	response, err := c.client.ListPersonalAuthorizations(ctx, request)
	if err != nil {
		return nil, err
	}
	var facts []domain.PersonalAuthorization
	for _, record := range response.Msg.GetAuthorizations() {
		// GitHub is the only supported service. Unknown providers cannot
		// establish a service relationship merely from their names.
		if record.GetProvider() != "github" {
			continue
		}
		facts = append(facts, domain.PersonalAuthorization{
			OwnerUserID: owner, AuthorizationID: record.GetId(), Provider: record.GetProvider(), Service: record.GetProvider(), SupportedTargets: record.GetSupportedTargets(),
		})
	}
	return facts, nil
}

// ListAccessibleRepositories reads the owner's live repository reach through
// Marketplace under the delegated owner identity.
func (c *Client) ListAccessibleRepositories(ctx context.Context, owner string) ([]domain.AccessibleRepository, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := delegated(ctx, owner, &marketplacepublicv1.ListAccessibleRepositoriesRequest{})
	if err != nil {
		return nil, err
	}
	response, err := c.client.ListAccessibleRepositories(ctx, request)
	if err != nil {
		return nil, err
	}
	var facts []domain.AccessibleRepository
	for _, record := range response.Msg.GetRepositories() {
		facts = append(facts, domain.AccessibleRepository{OwnerUserID: owner, Provider: record.GetProvider(), RepoID: record.GetId(), RepoName: record.GetName(),
			CloneURL: record.GetCloneUrl(), WebURL: record.GetWebUrl(), DefaultBranch: record.GetDefaultBranch()})
	}
	return facts, nil
}
