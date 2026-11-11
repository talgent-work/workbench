//go:build integration

package agent_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"google.golang.org/protobuf/proto"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A restarted module reads every Work as it was stored: its State, its
// capabilities and Harness Session, and the Subject still locates it.
func TestWorkStateSurvivesModuleRestart(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	states := []domain.WorkState{domain.WorkWorking, domain.WorkStopped, domain.WorkClosed}
	works := map[domain.WorkState]string{}
	for _, state := range states {
		works[state] = seedWork(t, m.db, definition, workSeed{subject: string(state), state: state, turns: 1, closeRequest: closeOf(state)}).ID
	}
	m.start()
	before := map[domain.WorkState]*agentprivatev1.GetWorkResponse{}
	for _, state := range states {
		before[state] = m.get(works[state])
	}
	m.restart()
	for _, state := range states {
		restored := m.get(works[state])
		require.Equal(t, before[state].State, restored.State, "the %s state survives module restart", state)
		require.True(t, proto.Equal(before[state].Capabilities, restored.Capabilities))
		require.Equal(t, before[state].ContextReference, restored.ContextReference)
		require.Equal(t, works[state], m.locate(definition.ID, string(state)).WorkId, "restart must retain the old binding rule")
	}
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, before[domain.WorkWorking].State)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, before[domain.WorkStopped].State)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_CLOSED, before[domain.WorkClosed].State)
}
