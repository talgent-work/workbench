// Package access reaches the authorities Observability asks before
// disclosing a Work's observations: the Project access decision and Agent's
// Work access.
package access

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	traceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/trace/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/trace/private/v1/traceprivatev1connect"

	"connectrpc.com/connect"

	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/pkg/authn"
)

type Config struct {
	// ProjectAccessAddress serves the Project access decision; empty when no
	// deployed context decides Project access.
	ProjectAccessAddress string `json:"project-access-address" toml:"project-access-address" yaml:"project-access-address"`
	// AgentAddress serves Agent's Work access, called with AgentControlToken.
	AgentAddress      string `json:"agent-address" toml:"agent-address" yaml:"agent-address"`
	AgentControlToken string `json:"agent-control-token" toml:"agent-control-token" yaml:"agent-control-token"`
}

func (c Config) Validate() error {
	if !absoluteHTTP(c.AgentAddress) {
		return errors.New("agent-address must be an absolute http(s) URL")
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

var _ query.ProjectAccessDecision = (*ProjectAccess)(nil)

// NewProjectAccess reaches the configured Project access decision, or returns
// none when no address is configured.
func NewProjectAccess(client *http.Client, cfg Config) (query.ProjectAccessDecision, error) {
	if strings.TrimSpace(cfg.ProjectAccessAddress) == "" {
		return nil, nil
	}
	if client == nil || !absoluteHTTP(cfg.ProjectAccessAddress) {
		return nil, errors.New("trace: Project access address and HTTP client are required")
	}
	address := strings.TrimRight(strings.TrimSpace(cfg.ProjectAccessAddress), "/")
	return &ProjectAccess{client: traceprivatev1connect.NewProjectAccessServiceClient(client, address)}, nil
}

func (a *ProjectAccess) AuthorizeProjectReader(ctx context.Context, readerID, projectID string) error {
	if err := check(readerID, projectID); err != nil {
		return err
	}
	req := connect.NewRequest(&traceprivatev1.DecideProjectAccessRequest{ProjectId: projectID})
	req.Header().Set(authn.HeaderServiceID, "trace")
	req.Header().Set(authn.HeaderDelegatedUserID, readerID)
	if _, err := a.client.DecideProjectAccess(ctx, req); err != nil {
		return authorityError(err)
	}
	return nil
}

// WorkAccess asks Agent whether a reader is the Work's owner or a Work Share
// holder. No admission is supplied, so a public Work admits no one else.
type WorkAccess struct {
	client agentprivatev1connect.WorkAccessServiceClient
	token  string
}

var _ query.WorkAccess = (*WorkAccess)(nil)

func NewWorkAccess(client *http.Client, cfg Config) (*WorkAccess, error) {
	if client == nil || !absoluteHTTP(cfg.AgentAddress) || strings.TrimSpace(cfg.AgentControlToken) == "" {
		return nil, errors.New("trace: Agent address, control token and HTTP client are required")
	}
	address := strings.TrimRight(strings.TrimSpace(cfg.AgentAddress), "/")
	return &WorkAccess{client: agentprivatev1connect.NewWorkAccessServiceClient(client, address), token: strings.TrimSpace(cfg.AgentControlToken)}, nil
}

func (a *WorkAccess) AuthorizeWorkReader(ctx context.Context, readerID, workID string) error {
	if err := check(readerID, workID); err != nil {
		return err
	}
	req := connect.NewRequest(&agentprivatev1.AdmitWorkReaderRequest{WorkId: workID, ReaderId: readerID})
	req.Header().Set("Authorization", "Bearer "+a.token)
	response, err := a.client.AdmitWorkReader(ctx, req)
	if err != nil {
		return authorityError(err)
	}
	switch response.Msg.GetBasis() {
	case agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE:
		return nil
	default:
		return query.ErrAuthorizationPermissionDenied
	}
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
