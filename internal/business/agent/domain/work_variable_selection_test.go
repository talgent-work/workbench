package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// selectingWork creates a Work of person (none when empty) offered A_TOKEN and
// B_TOKEN, with the start's choice among them.
func selectingWork(t *testing.T, person string, choice domain.PersonalVariableChoice) *domain.AgentWork {
	t.Helper()
	agentID := uuid.NewString()
	work, err := domain.NewAgentWork(domain.WorkInput{WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case"}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(),
		AgentID: agentID, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, RepresentedPerson: person, WorkLayout: domain.DefaultWorkLayout(),
		VariableInput: domain.WorkVariableInput{Offered: []string{"A_TOKEN", "B_TOKEN"}, Choice: choice},
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	return work
}

// Variable selection at creation: a start without a choice enables every
// offered variable, a choice the offered names it chose, an empty choice
// none, and a Work that represents no person none.
func TestANewWorkEnablesTheOfferedPersonalVariablesItsStartChose(t *testing.T) {
	require.Equal(t, []string{"A_TOKEN", "B_TOKEN"}, selectingWork(t, "owner", domain.PersonalVariableChoice{}).VariableSelection.Names)
	require.Equal(t, []string{"B_TOKEN"}, selectingWork(t, "owner", domain.PersonalVariableChoice{Chosen: true, Names: []string{"B_TOKEN", "NOT_KEPT"}}).VariableSelection.Names)
	require.Empty(t, selectingWork(t, "owner", domain.PersonalVariableChoice{Chosen: true}).VariableSelection.Names)
	require.Empty(t, selectingWork(t, "", domain.PersonalVariableChoice{}).VariableSelection.Names)
}

// Only a Work that represents a person enables Personal Variables, and its
// selection names each variable once, in name order.
func TestAWorkVariableSelectionNeedsARepresentedPersonAndNameOrder(t *testing.T) {
	work := selectingWork(t, "owner", domain.PersonalVariableChoice{})
	require.NoError(t, work.Validate())
	work.RepresentedPerson = ""
	require.ErrorIs(t, work.Validate(), domain.ErrInvalidWork)

	for _, names := range [][]string{{"B_TOKEN", "A_TOKEN"}, {"A_TOKEN", "A_TOKEN"}, {"not a name"}} {
		require.ErrorIs(t, domain.WorkVariableSelection{Names: names}.Validate(), domain.ErrInvalidWork, names)
	}
}
