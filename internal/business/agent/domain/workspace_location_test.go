package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Cloud Work's files are read from its Session Workspace in Storage, with
// what its sandbox mounts inside it.
func TestACloudWorkIsLocatedInItsSessionWorkspace(t *testing.T) {
	work := newSession(t)
	work.ReadOnlyMounts = []domain.ReadOnlyMount{{Name: "guides", Reference: "space-1"}}

	location, err := work.LocateWorkspace()
	require.NoError(t, err)
	require.False(t, location.ThroughComputer())
	require.Equal(t, work.ID, location.WorkID)
	require.Equal(t, "workspace-a", location.SessionWorkspace)
	require.Equal(t, []domain.ReadOnlyMount{{Name: "guides", Reference: "space-1"}}, location.ReadOnlyMounts)
}

// A Cloud Work without a Session Workspace, such as a project-scoped
// definition's, has nothing to read.
func TestACloudWorkWithoutASessionWorkspaceHasNoWorkspace(t *testing.T) {
	work := newSession(t)
	work.WorkspaceReference = ""

	_, err := work.LocateWorkspace()
	require.ErrorIs(t, err, domain.ErrWorkspaceNotCreated)
}

// A Managed Work's files stay on its machine: they are read in its directory
// through its Computer, whatever its creating caller named as a Session
// Workspace, and not during a Disconnection.
func TestAManagedWorkIsLocatedOnItsComputer(t *testing.T) {
	work, _ := queuedWork(t, time.Now())
	require.Equal(t, domain.ComputerManaged, work.ComputerKind)

	location, err := work.LocateWorkspace()
	require.NoError(t, err)
	require.True(t, location.ThroughComputer())
	require.Equal(t, work.ID, location.WorkID)
	require.Equal(t, managedComputer, location.ComputerID)
	require.Empty(t, location.SessionWorkspace)

	work.DisconnectedSince = time.Now()
	_, err = work.LocateWorkspace()
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
}

// A Work whose Computer kind is unknown is located nowhere.
func TestAWorkOfUnknownComputerKindIsLocatedNowhere(t *testing.T) {
	work := newSession(t)
	work.ComputerKind = ""

	_, err := work.LocateWorkspace()
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}

// The Work's Computer kind is what its Computer's admission answered at the
// first start; an answer naming another kind than the one chosen is not the
// chosen Computer's.
func TestTheAdmittingComputerNamesTheWorksComputerKind(t *testing.T) {
	input := machineLoginInput(true)
	work, err := domain.NewAgentWork(input, seatedFor(input))
	require.NoError(t, err)
	require.Equal(t, domain.ComputerManaged, work.ComputerKind)
	require.True(t, work.OnManagedComputer())

	_, err = domain.NewAgentWork(input, seated(input.AgentID))
	require.ErrorIs(t, err, domain.ErrComputerNotUsable)
}
