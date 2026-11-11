//go:build integration

package agent_test

import (
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Input whose hand-off to the input buffer fails is not accepted; a retry
// under the same identity is, and buffered input keeps the Work Working. A
// repeated report of the Turn that ended before cannot erase it.
func TestInputHandOffAfterATurnEnded(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkWorking, turns: 1}).ID
	m.start()
	input := uuid.NewString()
	m.port.inputError = errors.New("input buffer unavailable")
	require.Error(t, m.deliver(work, input, "continue after the result"))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, m.state(work), "input whose hand-off fails is not accepted")
	m.port.inputError = nil
	require.NoError(t, m.deliver(work, input, "continue after the result"), "a retry uses the same identity")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, m.state(work), "buffered input waits for the Harness without changing the state")
	require.NoError(t, m.report(&agentprivatev1.ReportRequest{ProcessId: launchedProcess, WorkId: work, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED, ObservationSequence: 1, TurnCompleted: true}))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, m.state(work), "a repeated old Turn end cannot erase later accepted input")
}

// Waiting follows platform admission of replies and input, independently of
// the Harness's protocol latency (assertWaitingResponseAdmission).
func TestWaitingFollowsResponseAdmission(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkWorking, turns: 1}).ID
	m.start()
	assertWaitingResponseAdmission(t, m.ctx, m.client, m.feedback, m.port, work)
}

// An input identity names one content: other content under a buffered
// identity is refused.
func TestConflictingInputIdentityIsRejected(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkWorking, turns: 1}).ID
	m.start()
	input := uuid.NewString()
	require.NoError(t, m.deliver(work, input, "finish before close"))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(m.deliver(work, input, "conflicting content")))
}
