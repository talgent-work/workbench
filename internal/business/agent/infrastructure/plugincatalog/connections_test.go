package plugincatalog_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// Repository facts are read under the delegated owner and attributed to that
// owner; Marketplace answers only for the authenticated delegation.
func TestAccessibleRepositoriesAreReadUnderTheDelegatedOwner(t *testing.T) {
	service := &repositoryService{}
	mux := http.NewServeMux()
	srv := connect.NewServer()
	marketplacepublicv1connect.RegisterMarketplaceServiceHandler(srv, service)
	connecthttp.Mount(mux, srv, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL})
	require.NoError(t, err)
	facts, err := client.ListAccessibleRepositories(t.Context(), "represented-user")
	require.NoError(t, err)
	require.Equal(t, []domain.AccessibleRepository{{OwnerID: "represented-user", Provider: "github",
		RepoID: "github:123", RepoName: "example/project", CloneURL: "https://github.com/example/project.git",
		WebURL: "https://github.com/example/project", DefaultBranch: "release"}}, facts)
	service.reject = true
	_, err = client.ListAccessibleRepositories(t.Context(), "represented-user")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

type repositoryService struct {
	marketplacepublicv1connect.UnimplementedMarketplaceServiceHandler
	reject bool
}

func (s *repositoryService) ListAccessibleRepositories(ctx context.Context, request *marketplacepublicv1.ListAccessibleRepositoriesRequest) (*marketplacepublicv1.ListAccessibleRepositoriesResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if s.reject || info.RequestHeader().Get(authn.HeaderServiceID) != "agent" || info.RequestHeader().Get(authn.HeaderDelegatedUserID) != "represented-user" {
		return nil, connect.NewError(connect.CodePermissionDenied, "")
	}
	return &marketplacepublicv1.ListAccessibleRepositoriesResponse{Repositories: []*marketplacepublicv1.AccessibleRepository{{Id: "github:123",
		Provider: "github", Name: "example/project", CloneUrl: "https://github.com/example/project.git",
		WebUrl: "https://github.com/example/project", DefaultBranch: "release"}}}, nil
}
