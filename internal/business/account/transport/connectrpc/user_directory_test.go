package connectrpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	accountprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/account/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/account/private/v1/accountprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/pkg/authn"
)

type directory struct{ users []query.User }

func (d directory) SearchByUsername(context.Context, string, int) ([]query.User, error) {
	return d.users, nil
}
func (d directory) GetByIDs(context.Context, []string) ([]query.User, error) { return d.users, nil }

// Only an in-cluster caller naming itself reads the directory; the gateway
// strips the service header from every external request.
func TestUserDirectoryAdmitsOnlyTrustedServices(t *testing.T) {
	app := application.NewApplication(application.Dependencies{Directory: directory{users: []query.User{{ID: "0190a0b2-0000-7000-8000-000000000001", Username: "octocat"}}}})
	path, handler := accountprivatev1connect.NewUserDirectoryServiceHandler(NewUserDirectory(app))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := accountprivatev1connect.NewUserDirectoryServiceClient(server.Client(), server.URL)

	_, err := client.SearchUsers(context.Background(), connect.NewRequest(&accountprivatev1.SearchUsersRequest{UsernameQuery: "octo"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	req := connect.NewRequest(&accountprivatev1.SearchUsersRequest{UsernameQuery: "octo"})
	req.Header().Set(authn.HeaderServiceID, "caller")
	resp, err := client.SearchUsers(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "octocat", resp.Msg.GetUsers()[0].GetUsername())

	tooMany := connect.NewRequest(&accountprivatev1.GetUsersRequest{UserIds: make([]string, 501)})
	tooMany.Header().Set(authn.HeaderServiceID, "caller")
	_, err = client.GetUsers(context.Background(), tooMany)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
