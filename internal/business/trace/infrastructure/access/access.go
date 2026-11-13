// Package access reaches the authorities Observability asks before
// disclosing observations: the access decision of the extension defining a
// Work Caller's kind, Agent's Work access and Work attribution, and Account's
// Application Ownership Query.
package access

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	traceprivatev1 "github.com/talgent-work/contracts/gen/go/trace/private/v1"
	"github.com/talgent-work/contracts/gen/go/trace/private/v1/traceprivatev1connect"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	accountprivatev1 "github.com/talgent-work/contracts/gen/go/account/private/v1"
	"github.com/talgent-work/contracts/gen/go/account/private/v1/accountprivatev1connect"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

type Config struct {
	// WorkCallerAccessAddresses maps each Work Caller kind an extension
	// defines to the address serving that extension's Work Caller access
	// decision; a kind left out is decided by none, so its Works are read by
	// no one. Empty in a deployment without extensions.
	WorkCallerAccessAddresses map[string]string `json:"work-caller-access-addresses" toml:"work-caller-access-addresses" yaml:"work-caller-access-addresses"`
	// AgentAddress serves Agent's Work access and Work attribution, called
	// with AgentControlToken.
	AgentAddress      string `json:"agent-address" toml:"agent-address" yaml:"agent-address"`
	AgentControlToken string `json:"agent-control-token" toml:"agent-control-token" yaml:"agent-control-token"`
	// AccountAddress serves Account's Application Ownership Query.
	AccountAddress string `json:"account-address" toml:"account-address" yaml:"account-address"`
}

func (c Config) Validate() error {
	if !absoluteHTTP(c.AgentAddress) {
		return errors.New("agent-address must be an absolute http(s) URL")
	}
	if !absoluteHTTP(c.AccountAddress) {
		return errors.New("account-address must be an absolute http(s) URL")
	}
	for kind, address := range c.WorkCallerAccessAddresses {
		if !extensionKind(kind) {
			return fmt.Errorf("work-caller-access-addresses: %q is not a Work Caller kind an extension defines", kind)
		}
		if !absoluteHTTP(address) {
			return fmt.Errorf("work-caller-access-addresses.%s must be an absolute http(s) URL", kind)
		}
	}
	if strings.TrimSpace(c.AgentControlToken) == "" {
		return errors.New("agent-control-token is required")
	}
	return nil
}

// extensionKind admits a Work Caller kind as Agent spells one, which is
// neither of the kinds Agent defines and whose Works it decides access to.
func extensionKind(kind string) bool {
	if kind == "" || len(kind) > 32 || kind == "person" || kind == "application" {
		return false
	}
	return strings.TrimFunc(kind, func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
	}) == ""
}

func absoluteHTTP(address string) bool {
	parsed, err := url.Parse(strings.TrimSpace(address))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// WorkCallerAccess asks the access decision of the extension defining a Work
// Caller's kind on the reader's behalf, one client per configured kind.
type WorkCallerAccess struct {
	clients map[string]traceprivatev1connect.WorkCallerAccessServiceClient
}

var _ query.WorkCallerAccessPort = (*WorkCallerAccess)(nil)

// NewWorkCallerAccess reaches the access decision configured for each kind.
func NewWorkCallerAccess(client *http.Client, cfg Config) (*WorkCallerAccess, error) {
	clients := make(map[string]traceprivatev1connect.WorkCallerAccessServiceClient, len(cfg.WorkCallerAccessAddresses))
	for kind, address := range cfg.WorkCallerAccessAddresses {
		if client == nil || !extensionKind(kind) || !absoluteHTTP(address) {
			return nil, fmt.Errorf("trace: the Work Caller access address of kind %q and an HTTP client are required", kind)
		}
		address = strings.TrimRight(strings.TrimSpace(address), "/")
		clients[kind] = traceprivatev1connect.NewWorkCallerAccessServiceClient(connect.NewClient(connecthttp.NewTransport(client, address, connecthttp.WithReadMaxBytes(0))))
	}
	return &WorkCallerAccess{clients: clients}, nil
}

// AuthorizeWorkCallerReader refuses a kind no extension decides here.
func (a *WorkCallerAccess) AuthorizeWorkCallerReader(ctx context.Context, readerID string, caller query.WorkCaller) error {
	if err := check(readerID, caller.ID); err != nil {
		return err
	}
	client, ok := a.clients[caller.Kind]
	if !ok {
		return query.ErrAuthorizationPermissionDenied
	}
	req := &traceprivatev1.DecideWorkCallerAccessRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: caller.Kind, Id: caller.ID}}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, serviceID)
	info.RequestHeader().Set(authn.HeaderDelegatedUserID, readerID)
	if _, err := client.DecideWorkCallerAccess(ctx, req); err != nil {
		return authorityError(err)
	}
	return nil
}

// Agent asks Agent whether a reader, a person or an Application, is the
// Work's owner or a Work Share holder, and which named Works an Application
// owns with their Work Labels. No admission is supplied, so a public Work
// admits no one else.
type Agent struct {
	access       agentprivatev1connect.WorkAccessServiceClient
	attributions agentprivatev1connect.WorkAttributionServiceClient
	token        string
}

var _ query.AgentPort = (*Agent)(nil)

func NewAgent(client *http.Client, cfg Config) (*Agent, error) {
	if client == nil || !absoluteHTTP(cfg.AgentAddress) || strings.TrimSpace(cfg.AgentControlToken) == "" {
		return nil, errors.New("trace: Agent address, control token and HTTP client are required")
	}
	address := strings.TrimRight(strings.TrimSpace(cfg.AgentAddress), "/")
	transport := connect.NewClient(connecthttp.NewTransport(client, address, connecthttp.WithReadMaxBytes(0)))
	return &Agent{
		access:       agentprivatev1connect.NewWorkAccessServiceClient(transport),
		attributions: agentprivatev1connect.NewWorkAttributionServiceClient(transport),
		token:        strings.TrimSpace(cfg.AgentControlToken),
	}, nil
}

func (a *Agent) AuthorizeWorkReader(ctx context.Context, readerID, workID string) error {
	if err := check(readerID, workID); err != nil {
		return err
	}
	req := &agentprivatev1.AdmitWorkReaderRequest{WorkId: workID, ReaderId: readerID}
	response, err := a.access.AdmitWorkReader(a.authorized(ctx), req)
	if err != nil {
		return authorityError(err)
	}
	switch response.GetBasis() {
	case agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_APPLICATION_OWNER:
		return nil
	default:
		return query.ErrAuthorizationPermissionDenied
	}
}

func (a *Agent) ListWorkAttributions(ctx context.Context, applicationID string, workIDs []string) ([]query.WorkAttribution, error) {
	if len(workIDs) == 0 {
		return nil, nil
	}
	response, err := a.attributions.ListWorkAttributions(a.authorized(ctx), &agentprivatev1.ListWorkAttributionsRequest{ApplicationId: applicationID, WorkIds: workIDs})
	if err != nil {
		return nil, errors.Join(query.ErrAuthorizationUnavailable, err)
	}
	works := make([]query.WorkAttribution, 0, len(response.GetWorks()))
	for _, work := range response.GetWorks() {
		works = append(works, query.WorkAttribution{WorkID: work.GetWorkId(), Labels: work.GetLabels()})
	}
	return works, nil
}

func (a *Agent) authorized(ctx context.Context) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+a.token)
	return ctx
}

// serviceID identifies Observability to Account's private API; the gateway
// strips it from every external request.
const serviceID = "trace"

// Account asks Account's Application Ownership Query.
type Account struct {
	client accountprivatev1connect.ApplicationOwnershipServiceClient
}

var _ query.AccountPort = (*Account)(nil)

func NewAccount(client *http.Client, cfg Config) (*Account, error) {
	if client == nil || !absoluteHTTP(cfg.AccountAddress) {
		return nil, errors.New("trace: Account address and HTTP client are required")
	}
	address := strings.TrimRight(strings.TrimSpace(cfg.AccountAddress), "/")
	return &Account{client: accountprivatev1connect.NewApplicationOwnershipServiceClient(connect.NewClient(connecthttp.NewTransport(client, address, connecthttp.WithReadMaxBytes(0))))}, nil
}

func (a *Account) GetApplication(ctx context.Context, applicationID string) (query.OwnedApplication, error) {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, serviceID)
	response, err := a.client.GetApplication(ctx, &accountprivatev1.GetApplicationRequest{ApplicationId: applicationID})
	switch {
	case connect.CodeOf(err) == connect.CodeNotFound, connect.CodeOf(err) == connect.CodeInvalidArgument:
		return query.OwnedApplication{}, query.ErrApplicationUnknown
	case err != nil:
		return query.OwnedApplication{}, err
	}
	found := response.GetApplication()
	return query.OwnedApplication{ID: found.GetApplicationId(), OwnerUserID: found.GetOwnerUserId(), Removed: found.GetRemoved()}, nil
}

func authorityError(err error) error {
	switch connect.CodeOf(err) {
	case connect.CodePermissionDenied, connect.CodeNotFound, connect.CodeInvalidArgument:
		return query.ErrAuthorizationPermissionDenied
	case connect.CodeUnauthenticated:
		return query.ErrAuthorizationUnauthenticated
	default:
		return errors.Join(query.ErrAuthorizationUnavailable, err)
	}
}

func check(readerID, objectID string) error {
	if strings.TrimSpace(readerID) == "" {
		return query.ErrAuthorizationUnauthenticated
	}
	if strings.TrimSpace(objectID) == "" {
		return query.ErrAuthorizationPermissionDenied
	}
	return nil
}
