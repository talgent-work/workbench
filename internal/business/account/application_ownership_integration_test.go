//go:build integration

package account_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"

	accountprivatev1 "github.com/talgent-work/contracts/gen/go/account/private/v1"
	"github.com/talgent-work/contracts/gen/go/account/private/v1/accountprivatev1connect"
	accountpublicv1 "github.com/talgent-work/contracts/gen/go/account/public/v1"
	"github.com/talgent-work/workbench/internal/business/account/domain"
	accountconnect "github.com/talgent-work/workbench/internal/business/account/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
)

// applicationOwnership serves Account's private Application Ownership Query
// as the controlplane mounts it.
func (h *harness) applicationOwnership() accountprivatev1connect.ApplicationOwnershipServiceClient {
	h.t.Helper()
	server := connectserver.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	accountprivatev1connect.RegisterApplicationOwnershipServiceHandler(server, accountconnect.NewApplicationOwnership(h.app))
	mux := http.NewServeMux()
	connectserver.Mount(mux, server)
	rpc := httptest.NewServer(mux)
	h.t.Cleanup(rpc.Close)
	return accountprivatev1connect.NewApplicationOwnershipServiceClient(connect.NewClient(connecthttp.NewTransport(rpc.Client(), rpc.URL)))
}

// A trusted context learns an Application's name and its Application Owner,
// also once the Application is removed; an unknown one is NotFound, and only
// in-cluster callers are answered.
func TestApplicationOwnershipAnswersTrustedContexts(t *testing.T) {
	h := newHarness(t)
	owner := newPerson()
	registered, err := h.applicationService().RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()
	ownership := h.applicationOwnership()

	read, err := ownership.GetApplication(asAgent(t.Context()), &accountprivatev1.GetApplicationRequest{ApplicationId: id})
	require.NoError(t, err)
	require.Equal(t, &accountprivatev1.OwnedApplication{ApplicationId: id, Name: "Support Desk", OwnerUserId: owner}, read.GetApplication())

	// Removal ends every secret, as the Remove behavior does.
	_, err = h.engine.Exec("UPDATE application_client_secret SET state = ? WHERE application_id = ?", int(domain.ClientSecretEnded), id)
	require.NoError(t, err)
	_, err = h.engine.Exec("UPDATE application SET state = ? WHERE id = ?", int(domain.ApplicationRemoved), id)
	require.NoError(t, err)
	read, err = ownership.GetApplication(asAgent(t.Context()), &accountprivatev1.GetApplicationRequest{ApplicationId: id})
	require.NoError(t, err)
	require.True(t, read.GetApplication().GetRemoved())
	require.Equal(t, owner, read.GetApplication().GetOwnerUserId(), "a removed Application keeps its owner")

	_, err = ownership.GetApplication(asAgent(t.Context()), &accountprivatev1.GetApplicationRequest{ApplicationId: newPerson()})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = ownership.GetApplication(t.Context(), &accountprivatev1.GetApplicationRequest{ApplicationId: id})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
