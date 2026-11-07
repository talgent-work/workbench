package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func startingWork(t *testing.T) (*domain.AgentWork, *executionPort) {
	t.Helper()
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"review"}, DefaultStartupAgent: "review:worker", BasePrompt: "retained instructions"}, []domain.PluginRelease{pluginRelease("review", "1")})
	require.NoError(t, err)
	id := uuid.NewString()
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: id, WorkspaceReference: "workspace", HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, Capabilities: snapshot}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: id, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true})
	port := follow(work)
	require.NoError(t, err)
	return work, port
}

func TestRejectedStartRetainsFailureWithoutInventingExitOrEnvironmentLoss(t *testing.T) {
	work, _ := startingWork(t)
	identity, conversation := work.ID, work.HarnessSessionReference
	snapshot := work.CapabilitySnapshot.Clone()
	rejected(t, work)
	require.Equal(t, domain.WorkFailed, work.State)
	require.True(t, work.HarnessProcess.StartRejected)
	require.False(t, work.HarnessProcess.Exited)
	require.False(t, work.HarnessProcess.EnvironmentLost)
	require.False(t, work.HoldsProcess())
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true, CurrentPlugins: currentReleases(work)}
	require.ErrorIs(t, work.Restart(admission), domain.ErrComputeReclamationPending)
	released(t, work)
	require.NoError(t, work.Restart(admission))
	require.Equal(t, domain.WorkStarting, work.State)
	require.True(t, work.HoldsProcess())
	require.False(t, work.HarnessProcess.StartRejected)
	require.Equal(t, identity, work.ID)
	require.Equal(t, conversation, work.HarnessSessionReference)
	require.Equal(t, snapshot, work.CapabilitySnapshot)
}

func TestStartRejectionCannotDischargeAnAlreadyStartedProcess(t *testing.T) {
	work, _ := startingWork(t)
	ready(t, work)
	_, err := work.ApplyExecutionReport(domain.ExecutionStartRejected{WorkID: work.ID, StartAdmittedAt: work.HarnessProcess.StartingSince})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	require.True(t, work.HoldsProcess())
	require.False(t, work.HarnessProcess.StartRejected)
}

func TestClosingRejectedStartWaitsForItsLocalObligationToBeRetired(t *testing.T) {
	work, _ := startingWork(t)
	rejected(t, work)
	require.NoError(t, work.Close("close", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosing, work.State)
	released(t, work)
	require.Equal(t, domain.WorkClosed, work.State)
	require.Len(t, closedEvents(work.Events.Drain()), 1)
}

// currentReleases reports the Work's retained releases as current, for starts
// that are not about upgrading Plugins.
func currentReleases(work *domain.AgentWork) []domain.PluginRelease {
	releases := make([]domain.PluginRelease, 0, len(work.CapabilitySnapshot.Plugins))
	for _, plugin := range work.CapabilitySnapshot.Plugins {
		releases = append(releases, plugin.Release)
	}
	return releases
}
