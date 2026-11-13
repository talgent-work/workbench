// Package account reaches Account's private API for Agent: the User Directory,
// the people a Standalone Work may be shared with, the Agent Account
// Command that issues a Work's Identity Token, and the Application Ownership
// Query.
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
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// Config locates Account's private API.
type Config struct {
	Address string `json:"address" yaml:"address" toml:"address"`
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Address) == "" {
		return errors.New("account address is required")
	}
	return nil
}

// serviceID identifies Agent to the private service; the gateway strips it
// from every external request.
const serviceID = "agent"

type Client struct {
	directory     accountprivatev1connect.UserDirectoryServiceClient
	agentAccounts accountprivatev1connect.AgentAccountServiceClient
	applications  accountprivatev1connect.ApplicationOwnershipServiceClient
}

var _ command.AccountPort = (*Client)(nil)

func NewClient(httpClient *http.Client, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client := connect.NewClient(connecthttp.NewTransport(httpClient, strings.TrimRight(strings.TrimSpace(cfg.Address), "/"), connecthttp.WithReadMaxBytes(0)))
	return &Client{
		directory:     accountprivatev1connect.NewUserDirectoryServiceClient(client),
		agentAccounts: accountprivatev1connect.NewAgentAccountServiceClient(client),
		applications:  accountprivatev1connect.NewApplicationOwnershipServiceClient(client),
	}, nil
}

func (c *Client) SearchUsers(ctx context.Context, query string, limit int) ([]command.DirectoryUser, error) {
	req := &accountprivatev1.SearchUsersRequest{UsernameQuery: query, Limit: uint32(max(limit, 0))}
	resp, err := c.directory.SearchUsers(asAgent(ctx), req)
	if err != nil {
		return nil, oops.With("operation", "account.search_users").Wrap(err)
	}
	return users(resp.GetUsers()), nil
}

func (c *Client) GetUsers(ctx context.Context, userIDs []string) ([]command.DirectoryUser, error) {
	req := &accountprivatev1.GetUsersRequest{UserIds: userIDs}
	resp, err := c.directory.GetUsers(asAgent(ctx), req)
	if err != nil {
		return nil, oops.With("operation", "account.get_users").Wrap(err)
	}
	return users(resp.GetUsers()), nil
}

// IssueWorkIdentityToken asks Account for the Work's agent Identity Token.
func (c *Client) IssueWorkIdentityToken(ctx context.Context, facts command.WorkIdentityFacts) (command.IdentityToken, error) {
	resp, err := c.agentAccounts.IssueWorkIdentityToken(asAgent(ctx), &accountprivatev1.IssueWorkIdentityTokenRequest{
		WorkId: facts.WorkID, AgentId: facts.AgentID, Purpose: facts.Purpose,
		WorkCallerKind: facts.WorkCaller.Kind, WorkCallerId: facts.WorkCaller.ID,
		WorkKeyKind: facts.WorkKey.Kind, WorkKeyId: facts.WorkKey.ID,
		FileHomeKind: facts.FileHome.Kind, FileHomeId: facts.FileHome.ID, RepresentedUserId: facts.RepresentedPerson,
		ApplicationId: facts.OwningApplication, WorkLabels: facts.WorkLabels,
	})
	if err != nil {
		// A request carries no token, so its error cannot leak one.
		return command.IdentityToken{}, oops.With("operation", "account.issue_work_identity_token").With("work_id", facts.WorkID).Wrap(err)
	}
	if resp.GetToken() == "" || resp.GetExpiresAt() == nil {
		return command.IdentityToken{}, oops.With("operation", "account.issue_work_identity_token").With("work_id", facts.WorkID).Errorf("account answered without a token")
	}
	return command.IdentityToken{Token: resp.GetToken(), ExpiresAt: resp.GetExpiresAt().AsTime()}, nil
}

// GetApplication asks Account's Application Ownership Query for one
// Application.
func (c *Client) GetApplication(ctx context.Context, applicationID string) (command.OwnedApplication, error) {
	resp, err := c.applications.GetApplication(asAgent(ctx), &accountprivatev1.GetApplicationRequest{ApplicationId: applicationID})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return command.OwnedApplication{}, command.ErrApplicationUnknown
	}
	if err != nil {
		return command.OwnedApplication{}, oops.With("operation", "account.get_application").With("application_id", applicationID).Wrap(err)
	}
	found := resp.GetApplication()
	return command.OwnedApplication{ID: found.GetApplicationId(), Name: found.GetName(), OwnerUserID: found.GetOwnerUserId(), Removed: found.GetRemoved()}, nil
}

// asAgent names Agent as the calling service.
func asAgent(ctx context.Context) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, serviceID)
	return ctx
}

func users(listed []*accountprivatev1.DirectoryUser) []command.DirectoryUser {
	out := make([]command.DirectoryUser, 0, len(listed))
	for _, user := range listed {
		displayName := user.GetDisplayName()
		if displayName == "" {
			displayName = user.GetUsername()
		}
		out = append(out, command.DirectoryUser{UserID: user.GetUserId(), Username: user.GetUsername(), DisplayName: displayName, AvatarURL: user.GetAvatarUrl()})
	}
	return out
}
