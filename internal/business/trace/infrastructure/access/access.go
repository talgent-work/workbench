// Package access reaches the authorities Observability asks before
// disclosing observations: the Project access decision, Agent's Work access
// and Work attribution, and Account's Application Ownership Query.
package access

import (
	"context"
	"errors"
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
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

type Config struct {
	// ProjectAccessAddress serves the Project access decision; empty when no
	// deployed context decides Project access.
	ProjectAccessAddress string `json:"project-access-address" toml:"project-access-address" yaml:"project-access-address"`
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
	if strings.TrimSpace(c.ProjectAccessAddress) != "" && !absoluteHTTP(c.ProjectAccessAddress) {
		return errors.New("project-access-address must be an absolute http(s) URL")
	}
	if strings.TrimSpace(c.AgentControlToken) == "" {
		return errors.New("agent-control-token is required")
	}
	return nil
}

func absoluteHTTP(address string) bool {
	parsed, err := url.Parse(strings.TrimSpace(address))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// ProjectAccess asks the Project access decision on the reader's behalf.
type ProjectAccess struct {
	client traceprivatev1connect.ProjectAccessServiceClient
}

var _ query.ProjectAccessPort = (*ProjectAccess)(nil)

// NewProjectAccess reaches the configured Project access decision, or returns
// none when no address is configured.
func NewProjectAccess(client *http.Client, cfg Config) (query.ProjectAccessPort, error) {
	if strings.TrimSpace(cfg.ProjectAccessAddress) == "" {
		return nil, nil
	}
	if client == nil || !absoluteHTTP(cfg.ProjectAccessAddress) {
		return nil, errors.New("trace: Project access address and HTTP client are required")
	}
	address := strings.TrimRight(strings.TrimSpace(cfg.ProjectAccessAddress), "/")
	return &ProjectAccess{client: traceprivatev1connect.NewProjectAccessServiceClient(connect.NewClient(connecthttp.NewTransport(client, address, connecthttp.WithReadMaxBytes(0))))}, nil
}

func (a *ProjectAccess) AuthorizeProjectReader(ctx context.Context, readerID, projectID string) error {
	if err := check(readerID, projectID); err != nil {
		return err
	}
	req := &traceprivatev1.DecideProjectAccessRequest{ProjectId: projectID}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, "trace")
	info.RequestHeader().Set(authn.HeaderDelegatedUserID, readerID)
	if _, err := a.client.DecideProjectAccess(ctx, req); err != nil {
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
	case agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE:
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
