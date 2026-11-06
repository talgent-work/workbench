package plugincatalog_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"github.com/talgent-work/workbench/pkg/authn"
)

// Repository facts are read under the delegated owner and attributed to that
// owner; Marketplace answers only for the authenticated delegation.
func TestAccessibleRepositoriesAreReadUnderTheDelegatedOwner(t *testing.T) {
	service := &repositoryService{}
	mux := http.NewServeMux()
	mux.Handle(marketplacepublicv1connect.NewMarketplaceServiceHandler(service))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL})
	require.NoError(t, err)
	facts, err := client.ListAccessibleRepositories(t.Context(), "represented-user")
	require.NoError(t, err)
	require.Equal(t, []domain.AccessibleRepository{{OwnerUserID: "represented-user", Provider: "github",
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

func (s *repositoryService) ListAccessibleRepositories(_ context.Context, request *connect.Request[marketplacepublicv1.ListAccessibleRepositoriesRequest]) (*connect.Response[marketplacepublicv1.ListAccessibleRepositoriesResponse], error) {
	if s.reject || request.Header().Get(authn.HeaderServiceID) != "agent" || request.Header().Get(authn.HeaderDelegatedUserID) != "represented-user" {
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	}
	return connect.NewResponse(&marketplacepublicv1.ListAccessibleRepositoriesResponse{Repositories: []*marketplacepublicv1.AccessibleRepository{{Id: "github:123",
		Provider: "github", Name: "example/project", CloneUrl: "https://github.com/example/project.git",
		WebUrl: "https://github.com/example/project", DefaultBranch: "release"}}}), nil
}
