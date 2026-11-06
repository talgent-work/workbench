package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func TestAWorkModelSelectsEitherAModelConfigurationOrTheMachineLogin(t *testing.T) {
	require.NoError(t, domain.WorkModelSelection{ConfigurationID: "glm-5"}.Validate())
	require.NoError(t, domain.MachineLoginModel().Validate())
	for _, invalid := range []domain.WorkModelSelection{{}, {ConfigurationID: "Not Valid"}, {ConfigurationID: "glm-5", MachineLogin: true}} {
		require.ErrorIs(t, invalid.Validate(), domain.ErrInvalidWork, invalid)
	}
}

func TestTheMachineLoginIsOnlyForAManagedComputer(t *testing.T) {
	require.NoError(t, domain.MachineLoginModel().AdmitOn(true))
	require.ErrorIs(t, domain.MachineLoginModel().AdmitOn(false), domain.ErrMachineLoginNotManaged)
	require.NoError(t, domain.WorkModelSelection{ConfigurationID: "glm-5"}.AdmitOn(false))
	require.NoError(t, domain.WorkModelSelection{ConfigurationID: "glm-5"}.AdmitOn(true))
}

func machineLoginInput(managed bool) domain.WorkInput {
	return domain.WorkInput{WorkModel: domain.MachineLoginModel(), ComputerManaged: managed, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: uuid.NewString(), WorkspaceReference: "workspace", HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}}
}

func seated(agentID string) domain.StartAdmission {
	return domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}}
}

// A Work on the machine's Claude login has no Model Configuration to be
// withdrawn: its establishment and every later start skip that admission.
func TestAMachineLoginWorkIsAdmittedWithoutAModelConfiguration(t *testing.T) {
	ctx := context.Background()
	input := machineLoginInput(true)
	port := &executionPort{}
	work, err := domain.NewAgentWork(ctx, input, seated(input.AgentID), port)
	require.NoError(t, err)
	require.Equal(t, domain.MachineLoginModel(), port.starts[0].WorkModel)
	require.NoError(t, work.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, work.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, work.Stop(ctx, "stop-1", port))
	require.NoError(t, work.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, work.ConfirmComputeReclaimed())
	require.NoError(t, work.Resume(ctx, seated(input.AgentID), port))
	require.Equal(t, domain.WorkStarting, work.State)
}

func TestAWorkKeepsOnlyAModelItsComputerCanUse(t *testing.T) {
	input := machineLoginInput(false)
	port := &executionPort{}
	work, err := domain.NewAgentWork(context.Background(), input, seated(input.AgentID), port)
	require.ErrorIs(t, err, domain.ErrMachineLoginNotManaged)
	require.Nil(t, work)
	require.Empty(t, port.starts, "a rejected Work requests no execution")

	// A Managed Computer may also run a Work on a provided Model Configuration.
	input = machineLoginInput(true)
	input.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	admission := seated(input.AgentID)
	admission.ModelConfigurationProvided = true
	work, err = domain.NewAgentWork(context.Background(), input, admission, port)
	require.NoError(t, err)
	require.Equal(t, input.WorkModel, port.starts[0].WorkModel)
}

// On a Managed Computer a Provider-authorized Connector is unavailable: the
// Work is established without it, and with it on a Cloud.
func TestAManagedWorkLeavesOutProviderAuthorizedConnectors(t *testing.T) {
	for _, managed := range []bool{true, false} {
		input := machineLoginInput(managed)
		if !managed {
			input.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
		}
		input.Capabilities.Connectors = append(input.Capabilities.Connectors, connectorcatalogtest.GitHub().SelectedConnector)
		admission := seated(input.AgentID)
		admission.ModelConfigurationProvided = true
		port := &executionPort{}
		work, err := domain.NewAgentWork(context.Background(), input, admission, port)
		require.NoError(t, err, "an unavailable Connector does not reject creation")
		names := []string{}
		for _, connector := range work.CapabilitySnapshot.Connectors {
			names = append(names, connector.Name)
		}
		if managed {
			require.Equal(t, []string{"talgent"}, names)
		} else {
			require.Equal(t, []string{"talgent", "github"}, names)
		}
		require.Len(t, input.Capabilities.Connectors, 2, "the supplied capabilities are not changed")
	}
}

// Start options say which Connectors a Work on a Managed Computer leaves out,
// by the rule that leaves them out of its snapshot.
func TestConnectorOptionsReportManagedAvailability(t *testing.T) {
	github := connectorcatalogtest.GitHub()
	options, err := domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required(), github}, EnabledConnectors: []string{github.ID}}.ConnectorOptions()
	require.NoError(t, err)
	require.Equal(t, []domain.ConnectorOption{{Name: "github", IncludedByDefault: true, UnavailableOnManaged: true}, {Name: "talgent", Required: true, IncludedByDefault: true}}, options)
}
