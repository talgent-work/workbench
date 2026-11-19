// Package account reaches Account for Marketplace: its Application Ownership
// Query, which admits an Application Owner to the Application's installations,
// Connectors and Provider Authorizations, and its User Directory, which names
// Connector publishers.
package account

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/samber/oops"

	accountprivatev1 "github.com/talgent-work/contracts/gen/go/account/private/v1"
	"github.com/talgent-work/contracts/gen/go/account/private/v1/accountprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// Config locates Account's private API.
type Config struct {
	Address string `json:"address" yaml:"address" toml:"address"`
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Address) == "" {
		return errors.New("marketplace: account address is required")
	}
	return nil
}

// serviceID identifies Marketplace to the private service; the gateway
// strips it from every external request.
const serviceID = "marketplace"

type Client struct {
	applications accountprivatev1connect.ApplicationOwnershipServiceClient
	directory    accountprivatev1connect.UserDirectoryServiceClient
}

var (
	_ application.AccountPort        = (*Client)(nil)
	_ application.PublisherDirectory = (*Client)(nil)
)

func NewClient(httpClient *http.Client, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client := connect.NewClient(connecthttp.NewTransport(httpClient, strings.TrimRight(strings.TrimSpace(cfg.Address), "/")))
	return &Client{applications: accountprivatev1connect.NewApplicationOwnershipServiceClient(client), directory: accountprivatev1connect.NewUserDirectoryServiceClient(client)}, nil
}

// GetApplication asks Account who owns one Application.
func (c *Client) GetApplication(ctx context.Context, applicationID string) (application.OwnedApplication, error) {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, serviceID)
	resp, err := c.applications.GetApplication(ctx, &accountprivatev1.GetApplicationRequest{ApplicationId: applicationID})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return application.OwnedApplication{}, application.ErrApplicationUnknown
	}
	if err != nil {
		return application.OwnedApplication{}, oops.With("operation", "account.get_application").With("application_id", applicationID).Wrap(err)
	}
	found := resp.GetApplication()
	return application.OwnedApplication{ID: found.GetApplicationId(), OwnerID: found.GetOwnerUserId(), Removed: found.GetRemoved()}, nil
}

// PersonNames reads the display names of the known people among ids, each
// its GitHub display name or else its username.
func (c *Client) PersonNames(ctx context.Context, ids []string) (map[string]string, error) {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, serviceID)
	resp, err := c.directory.GetUsers(ctx, &accountprivatev1.GetUsersRequest{UserIds: ids})
	if err != nil {
		return nil, oops.With("operation", "account.get_users").Wrap(err)
	}
	names := make(map[string]string, len(resp.GetUsers()))
	for _, user := range resp.GetUsers() {
		name := strings.TrimSpace(user.GetDisplayName())
		if name == "" {
			name = user.GetUsername()
		}
		names[user.GetUserId()] = name
	}
	return names, nil
}

// ApplicationName reads the name an Application's Owner manages it by.
func (c *Client) ApplicationName(ctx context.Context, applicationID string) (string, error) {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, serviceID)
	resp, err := c.applications.GetApplication(ctx, &accountprivatev1.GetApplicationRequest{ApplicationId: applicationID})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return "", application.ErrApplicationUnknown
	}
	if err != nil {
		return "", oops.With("operation", "account.get_application").With("application_id", applicationID).Wrap(err)
	}
	return resp.GetApplication().GetName(), nil
}
