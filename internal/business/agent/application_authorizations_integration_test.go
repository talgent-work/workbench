//go:build integration

package agent_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ownedConnections is Marketplace's answer for each owner, a person or an
// Application: one GitHub authorization reaching one repository of its own.
// An owner without one is refused the listing, as Marketplace refuses it.
type ownedConnections map[string]string

func (c ownedConnections) ListPersonalAuthorizations(_ context.Context, owner string) ([]domain.PersonalAuthorization, error) {
	id, ok := c[owner]
	if !ok {
		return nil, nil
	}
	return []domain.PersonalAuthorization{{OwnerUserID: owner, AuthorizationID: id, Provider: "github", Service: "github"}}, nil
}

func (c ownedConnections) ListAccessibleRepositories(_ context.Context, owner string) ([]domain.AccessibleRepository, error) {
	if _, ok := c[owner]; !ok {
		return nil, domain.ErrRepositoryAuthorizationMissing
	}
	return []domain.AccessibleRepository{{OwnerUserID: owner, Provider: "github", RepoID: "github:" + owner, RepoName: owner + "/project",
		CloneURL: "https://github.com/example/" + owner + ".git", WebURL: "https://github.com/example/" + owner, DefaultBranch: "main"}}, nil
}

// An Application's Works check out repositories and use credentials under
// the Application's own Provider Authorization, never its Application
// Owner's; an Application without one is told so at start.
func TestAnApplicationsWorksUseTheApplicationsOwnAuthorization(t *testing.T) {
	owner := moduleApplications[supportDesk].OwnerUserID
	deskAuthorization, ownerAuthorization := uuid.NewString(), uuid.NewString()
	m := newLifecycleModule(t)
	m.catalog = commandtest.StartCatalog(modulePluginCatalog{}, ownedConnections{supportDesk: deskAuthorization, owner: ownerAuthorization})
	m.start()
	desk := m.as("application", supportDesk)

	listed, err := desk.works.ListWorkStartRepositories(desk.ctx, &agentpublicv2.ListWorkStartRepositoriesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.GetRepositories().GetRepositories(), 1)
	require.Equal(t, "github:"+supportDesk, listed.GetRepositories().GetRepositories()[0].GetId(), "it lists what its own authorization reaches")

	started, err := desk.works.StartWork(desk.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Fix the build",
		RepositorySelections: []*agentpublicv1.RepositorySelection{{RepositoryId: "github:" + supportDesk}}})
	require.NoError(t, err)
	work, err := m.app.Works.Get(m.ctx, started.GetWorkId())
	require.NoError(t, err)
	require.Equal(t, supportDesk, work.OwningApplication)
	require.Empty(t, work.RepresentedPerson)
	require.Equal(t, supportDesk, work.RepositorySelection.OwnerUserID)
	require.Equal(t, supportDesk, work.AuthorizationSelection.OwnerUserID)
	require.Equal(t, []domain.WorkServiceAuthorization{{AuthorizationID: deskAuthorization, Provider: "github", Service: "github"}}, work.AuthorizationSelection.Authorizations,
		"its owner's authorization is never selected")
	require.Equal(t, supportDesk, work.LaunchSpecification().AuthorizationOwner())

	_, err = desk.works.StartWork(desk.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Fix the owner's build",
		RepositorySelections: []*agentpublicv1.RepositorySelection{{RepositoryId: "github:" + owner}}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "its owner's repositories are out of its reach")

	billing := m.as("application", billingBot)
	_, err = billing.works.StartWork(billing.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Fix the build",
		RepositorySelections: []*agentpublicv1.RepositorySelection{{RepositoryId: "github:" + billingBot}}})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.ErrorContains(t, err, "no usable Provider Authorization")
	unlisted, err := billing.works.ListWorkStartRepositories(billing.ctx, &agentpublicv2.ListWorkStartRepositoriesRequest{})
	require.NoError(t, err)
	require.True(t, unlisted.GetRepositories().GetRepositoriesUnavailable())
}
