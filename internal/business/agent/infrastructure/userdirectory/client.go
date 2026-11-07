// Package userdirectory offers Account's User Directory to Agent as the people
// a Work without a Project may be shared with.
package userdirectory

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/samber/oops"

	accountprivatev1 "github.com/talgent-work/contracts/gen/account/private/v1"
	"github.com/talgent-work/contracts/gen/account/private/v1/accountprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// Config locates Account's private User Directory service.
type Config struct {
	Address string `json:"address" yaml:"address" toml:"address"`
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Address) == "" {
		return errors.New("user directory address is required")
	}
	return nil
}

// serviceID identifies Agent to the private service; the gateway strips it
// from every external request.
const serviceID = "agent"

type Client struct {
	directory accountprivatev1connect.UserDirectoryServiceClient
}

var _ personalwork.UserDirectory = (*Client)(nil)

func NewClient(httpClient *http.Client, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Client{directory: accountprivatev1connect.NewUserDirectoryServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, strings.TrimRight(strings.TrimSpace(cfg.Address), "/"), connecthttp.WithReadMaxBytes(0))))}, nil
}

func (c *Client) SearchUsers(ctx context.Context, query string, limit int) ([]personalwork.DirectoryUser, error) {
	req := &accountprivatev1.SearchUsersRequest{UsernameQuery: query, Limit: uint32(max(limit, 0))}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, serviceID)
	resp, err := c.directory.SearchUsers(ctx, req)
	if err != nil {
		return nil, oops.With("operation", "account.search_users").Wrap(err)
	}
	return users(resp.GetUsers()), nil
}

func (c *Client) GetUsers(ctx context.Context, userIDs []string) ([]personalwork.DirectoryUser, error) {
	req := &accountprivatev1.GetUsersRequest{UserIds: userIDs}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, serviceID)
	resp, err := c.directory.GetUsers(ctx, req)
	if err != nil {
		return nil, oops.With("operation", "account.get_users").Wrap(err)
	}
	return users(resp.GetUsers()), nil
}

func users(listed []*accountprivatev1.DirectoryUser) []personalwork.DirectoryUser {
	out := make([]personalwork.DirectoryUser, 0, len(listed))
	for _, user := range listed {
		displayName := user.GetDisplayName()
		if displayName == "" {
			displayName = user.GetUsername()
		}
		out = append(out, personalwork.DirectoryUser{UserID: user.GetUserId(), Username: user.GetUsername(), DisplayName: displayName, AvatarURL: user.GetAvatarUrl()})
	}
	return out
}
