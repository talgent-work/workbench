package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func workWithInitialTaskInput(t *testing.T, input *domain.InitialTaskInput) *domain.AgentWork {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}})
	require.NoError(t, err)
	work, err := domain.NewAgentWork(domain.WorkInput{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(),
		AgentID: agent.ID, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}},
		RepresentedPerson: "user-home", WorkLayout: domain.DefaultWorkLayout(), InitialTaskInput: input,
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	return work
}

func TestInitialTaskInputRequiresIdentityAndText(t *testing.T) {
	_, err := domain.NewInitialTaskInput("not-a-uuid", "Do the work")
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	_, err = domain.NewInitialTaskInput(uuid.NewString(), "  ")
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}

// A new Work holds its initial task input until it reaches the input buffer;
// no later input or answer is accepted ahead of it (Initial task input order).
func TestInitialTaskInputIsHandedOffBeforeAnyLaterInput(t *testing.T) {
	initial, err := domain.NewInitialTaskInput(uuid.NewString(), "Do the work")
	require.NoError(t, err)
	work := workWithInitialTaskInput(t, &initial)

	pending, ok := work.PendingInitialTaskInput()
	require.True(t, ok)
	require.Equal(t, initial, pending)
	require.ErrorIs(t, work.AcceptInput(false), domain.ErrInitialTaskInputPending)
	require.ErrorIs(t, work.AcceptResponse(false), domain.ErrInitialTaskInputPending)

	require.False(t, work.HandOffInitialTaskInput(uuid.NewString()), "another input identity hands off nothing")
	_, ok = work.PendingInitialTaskInput()
	require.True(t, ok)

	require.True(t, work.HandOffInitialTaskInput(initial.ID))
	_, ok = work.PendingInitialTaskInput()
	require.False(t, ok, "a handed-off initial task input is no longer held")
	require.False(t, work.HandOffInitialTaskInput(initial.ID), "a repeated hand-off changes nothing")
	require.NoError(t, work.AcceptInput(false), "later input follows the handed-off initial task input")
}

func TestWorkWithoutInitialTaskInputAcceptsInputAtOnce(t *testing.T) {
	work := workWithInitialTaskInput(t, nil)
	_, ok := work.PendingInitialTaskInput()
	require.False(t, ok)
	require.NoError(t, work.AcceptInput(false))
}

// A running Work whose initial task input has not reached its buffer still
// owes it, so a close waits for the hand-off.
func TestRunningWorkHoldingItsInitialTaskInputIsNotClosed(t *testing.T) {
	initial, err := domain.NewInitialTaskInput(uuid.NewString(), "Do the work")
	require.NoError(t, err)
	work := workWithInitialTaskInput(t, &initial)
	ready(t, work)
	require.Equal(t, domain.WorkWorking, work.State)
	require.ErrorIs(t, work.Close("finish", domain.ClosureFacts{}), domain.ErrWorkTransition)
	require.True(t, work.HandOffInitialTaskInput(initial.ID))
	require.NoError(t, work.Close("finish", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkStopping, work.State)
}

// Closing empties input: an initial task input a failed first start never
// handed off is dropped when the Work closes.
func TestClosingDropsAnInitialTaskInputNotHandedOff(t *testing.T) {
	initial, err := domain.NewInitialTaskInput(uuid.NewString(), "Do the work")
	require.NoError(t, err)
	work := workWithInitialTaskInput(t, &initial)
	missedDeadline(t, work, "Startup Deadline reached before the execution environment was ready.")
	require.Equal(t, domain.WorkFailed, work.State)
	require.NoError(t, work.Close("close", domain.ClosureFacts{}))
	_, pending := work.PendingInitialTaskInput()
	require.False(t, pending)
}
