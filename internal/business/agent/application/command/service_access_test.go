package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var githubRequirements = []command.ServiceRequirement{{Provider: "github", Service: "github", Target: "github-api"}, {Provider: "github", Service: "github", Target: "github-remote-mcp"}}

func TestServiceAccessUsesCurrentCredentialsWithoutRepositoriesOrPlugins(t *testing.T) {
	f, supplier := serviceFixture(t, true)
	app := command.NewServiceAccess(repositoryWorkStore{f: f}, supplier)
	for _, token := range []string{"initial-token", "renewed-token"} {
		supplier.token = token
		result, err := app.Resolve(t.Context(), "owner", f.work.ID, githubRequirements)
		require.NoError(t, err)
		require.Empty(t, result.Unavailable)
		require.Len(t, result.Credentials, 2)
		for _, material := range result.Credentials {
			require.Equal(t, token, material.Token)
			require.Equal(t, f.work.AuthorizationSelection.Authorizations[0].AuthorizationID, material.AuthorizationID)
		}
	}
	require.Empty(t, f.work.RepositorySelection.Repositories)
	require.Empty(t, f.work.CapabilitySnapshot.Plugins)
	for _, request := range supplier.requests {
		require.Equal(t, "owner", request.OwnerID)
		require.Equal(t, f.work.AuthorizationSelection.Authorizations[0].AuthorizationID, request.AuthorizationID)
	}
}

func TestServiceAccessDegradesAndRecoversExistingAuthorization(t *testing.T) {
	f, supplier := serviceFixture(t, true)
	app := command.NewServiceAccess(repositoryWorkStore{f: f}, supplier)
	for _, reason := range []string{"revoked", "reconnect_required", "unavailable"} {
		supplier.err = &command.ServiceCredentialUnavailable{Reason: reason}
		result, err := app.Resolve(t.Context(), "owner", f.work.ID, githubRequirements)
		require.NoError(t, err, "ordinary external unavailability does not fail the Work")
		require.Empty(t, result.Credentials)
		require.Len(t, result.Unavailable, 2)
		require.Equal(t, reason, result.Unavailable[0].Reason)
		require.Equal(t, domain.WorkStarting, f.work.State)
	}
	supplier.err, supplier.token = nil, "reauthorized-token"
	result, err := app.Resolve(t.Context(), "owner", f.work.ID, githubRequirements)
	require.NoError(t, err)
	require.Len(t, result.Credentials, 2)
	require.Equal(t, "reauthorized-token", result.Credentials[0].Token)
}

func TestServiceAccessNeverAddsAnUnselectedAuthorization(t *testing.T) {
	f, supplier := serviceFixture(t, false)
	app := command.NewServiceAccess(repositoryWorkStore{f: f}, supplier)
	for _, token := range []string{"", "new-personal-authorization-token"} {
		supplier.token = token
		result, err := app.Resolve(t.Context(), "owner", f.work.ID, githubRequirements)
		require.NoError(t, err)
		require.Empty(t, result.Credentials)
		require.Len(t, result.Unavailable, 2)
		require.Equal(t, "not_selected", result.Unavailable[0].Reason)
		require.Empty(t, supplier.requests)
	}
}

func TestServiceAccessRefusesAnotherOwnerAndLateMaterialAfterStop(t *testing.T) {
	f, supplier := serviceFixture(t, true)
	app := command.NewServiceAccess(repositoryWorkStore{f: f}, supplier)
	_, err := app.Resolve(t.Context(), "starter", f.work.ID, githubRequirements)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.Empty(t, supplier.requests)
	readyWork(t, f.work)
	supplier.duringSupply = func() { require.NoError(t, f.work.Stop("stop", false)) }
	result, err := app.Resolve(t.Context(), "owner", f.work.ID, githubRequirements[:1])
	require.ErrorIs(t, err, domain.ErrWorkTransition)
	require.Empty(t, result.Credentials)
}

func serviceFixture(t *testing.T, selected bool) (*repositoryAccessFixture, *serviceSupplier) {
	t.Helper()
	agentID := uuid.NewString()
	input := domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: agentID, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, RepresentedPerson: "owner", WorkLayout: domain.DefaultWorkLayout()}
	// A Work holds a Provider's authorization only when the owner had one usable at creation.
	input.AuthorizationInput = domain.WorkAuthorizationInput{OwnerID: "owner"}
	if selected {
		input.AuthorizationInput.Available = []domain.PersonalAuthorization{{OwnerID: "owner", AuthorizationID: uuid.NewString(), Provider: "github", Service: "github"}}
	}
	work, err := domain.NewAgentWork(input, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	return &repositoryAccessFixture{work: work}, &serviceSupplier{token: "current-token"}
}

type serviceSupplier struct {
	command.MarketplacePort
	token        string
	err          error
	requests     []command.ServiceCredentialRequest
	duringSupply func()
}

func (s *serviceSupplier) SupplyServiceCredential(_ context.Context, input command.ServiceCredentialRequest) (command.ServiceCredentialMaterial, error) {
	s.requests = append(s.requests, input)
	if s.duringSupply != nil {
		s.duringSupply()
	}
	if s.err != nil {
		return command.ServiceCredentialMaterial{}, s.err
	}
	return command.ServiceCredentialMaterial{Token: s.token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
