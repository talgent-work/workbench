package plugincatalog

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect/v2"

	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (c *Client) ListPersonalAuthorizations(ctx context.Context, owner string) ([]domain.PersonalAuthorization, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx, _, err := delegated(ctx, owner)
	if err != nil {
		return nil, err
	}
	response, err := c.client.ListPersonalAuthorizations(ctx, &marketplacepublicv1.ListPersonalAuthorizationsRequest{})
	if err != nil {
		return nil, err
	}
	var facts []domain.PersonalAuthorization
	for _, record := range response.GetAuthorizations() {
		// GitHub is the only supported service. Unknown providers cannot
		// establish a service relationship merely from their names.
		if record.GetProvider() != "github" {
			continue
		}
		facts = append(facts, domain.PersonalAuthorization{
			OwnerID: owner, AuthorizationID: record.GetId(), Provider: record.GetProvider(), Service: record.GetProvider(), SupportedTargets: record.GetSupportedTargets(),
		})
	}
	return facts, nil
}

// ListAccessibleRepositories reads the owner's live repository reach through
// Marketplace under the delegated owner identity.
func (c *Client) ListAccessibleRepositories(ctx context.Context, owner string) ([]domain.AccessibleRepository, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx, _, err := delegated(ctx, owner)
	if err != nil {
		return nil, err
	}
	response, err := c.client.ListAccessibleRepositories(ctx, &marketplacepublicv1.ListAccessibleRepositoriesRequest{})
	// Marketplace refuses the listing while the owner has no usable
	// authorization: none connected, or one that needs reconnecting.
	if connect.CodeOf(err) == connect.CodeFailedPrecondition {
		return nil, errors.Join(domain.ErrRepositoryAuthorizationMissing, err)
	}
	if err != nil {
		return nil, err
	}
	var facts []domain.AccessibleRepository
	for _, record := range response.GetRepositories() {
		facts = append(facts, domain.AccessibleRepository{OwnerID: owner, Provider: record.GetProvider(), RepoID: record.GetId(), RepoName: record.GetName(),
			CloneURL: record.GetCloneUrl(), WebURL: record.GetWebUrl(), DefaultBranch: record.GetDefaultBranch()})
	}
	return facts, nil
}
