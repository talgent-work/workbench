package nativeprocess_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

// Captured from Claude Code 2.1.266 (the sandbox's Harness) and 2.1.304: a
// foreground and a background SubAgent completing, a SubAgent delegating once
// more, and a background SubAgent stopped with TaskStop. 2.1.266 names no
// parent task; the spawn depth alone tells the nested delegate apart.
func TestNativeTaskEventsProduceTheMainAgentsSubAgentsAcrossCheckpointReload(t *testing.T) {
	for _, capture := range []struct {
		release string
		first   command.SubAgent
		last    []command.SubAgent
	}{
		{"2_1_266", command.SubAgent{ID: "abd6784b0eca9193c", Type: "general-purpose", Task: "bg echo", Status: "running"}, []command.SubAgent{
			{ID: "abd6784b0eca9193c", Type: "general-purpose", Task: "bg echo", Status: "completed"},
			{ID: "a3ae5ec36d8d9d756", Type: "general-purpose", Task: "count files", Status: "completed"},
			{ID: "a4601ed954d3d1d2a", Type: "general-purpose", Task: "nest probe", Status: "completed"},
			{ID: "a917d464cbb99d031", Type: "general-purpose", Task: "long sleeper", Status: "stopped"},
		}},
		{"2_1_304", command.SubAgent{ID: "ae12c333fec0b4e9b", Type: "general-purpose", Task: "count files", Status: "running"}, []command.SubAgent{
			{ID: "ae12c333fec0b4e9b", Type: "general-purpose", Task: "count files", Status: "completed"},
			{ID: "a5e1ef4778da1a8a5", Type: "general-purpose", Task: "bg echo", Status: "completed"},
			{ID: "ab626f549bdb2c644", Type: "general-purpose", Task: "nest probe", Status: "completed"},
			{ID: "af03a5b7f2071b98a", Type: "general-purpose", Task: "long sleeper", Status: "stopped"},
		}},
	} {
		t.Run(capture.release, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/subagents_claude_" + capture.release + ".json")
			require.NoError(t, err)
			var frames []nativeprocess.Message
			require.NoError(t, json.Unmarshal(raw, &frames))
			var state nativeprocess.ProtocolState
			var snapshots [][]command.SubAgent
			for _, frame := range frames {
				if subAgents, updated := state.ObserveSubAgents(&frame); updated {
					snapshots = append(snapshots, subAgents)
				}
				checkpoint, err := json.Marshal(state)
				require.NoError(t, err)
				state = nativeprocess.ProtocolState{}
				require.NoError(t, json.Unmarshal(checkpoint, &state))
			}
			require.Len(t, snapshots, 8, "the nested delegate's start and end change nothing")
			require.Equal(t, []command.SubAgent{capture.first}, snapshots[0])
			require.Equal(t, capture.last, snapshots[7])
		})
	}
}

// Tasks other than SubAgents are not SubAgents, a Harness that says killed
// means stopped, a status this platform does not know changes nothing, and a
// long task is bounded.
func TestSubAgentsKeepOnlyKnownDelegationsAndStates(t *testing.T) {
	var state nativeprocess.ProtocolState
	observe := func(frame string) ([]command.SubAgent, bool) {
		var message nativeprocess.Message
		require.NoError(t, json.Unmarshal([]byte(frame), &message))
		return state.ObserveSubAgents(&message)
	}
	_, updated := observe(`{"type":"system","subtype":"task_started","task_id":"shell","tool_use_id":"t0","description":"sleep","task_type":"local_bash"}`)
	require.False(t, updated)
	_, updated = observe(`{"type":"system","subtype":"task_notification","task_id":"shell","tool_use_id":"t0","status":"completed"}`)
	require.False(t, updated)
	_, updated = observe(`{"type":"system","subtype":"task_started","task_id":"plan","tool_use_id":"t1","description":"plan rollout","subagent_type":"Plan","spawn_depth":1,"task_type":"local_agent"}`)
	require.True(t, updated)
	_, updated = observe(`{"type":"system","subtype":"task_started","task_id":"inner","tool_use_id":"t2","description":"look up","subagent_type":"Explore","spawn_depth":2,"task_type":"local_agent"}`)
	require.False(t, updated)
	_, updated = observe(`{"type":"system","subtype":"task_started","task_id":"review","tool_use_id":"t3","description":"review","task_type":"local_agent","spawn_depth":1}`)
	require.True(t, updated)
	subAgents, updated := observe(`{"type":"system","subtype":"task_notification","task_id":"plan","tool_use_id":"t1","status":"failed"}`)
	require.True(t, updated)
	require.Equal(t, []command.SubAgent{{ID: "plan", Type: "Plan", Task: "plan rollout", Status: "failed"}, {ID: "review", Task: "review", Status: "running"}}, subAgents)
	subAgents, updated = observe(`{"type":"system","subtype":"task_notification","task_id":"review","tool_use_id":"t3","status":"killed"}`)
	require.True(t, updated)
	require.Equal(t, "stopped", subAgents[1].Status)
	_, updated = observe(`{"type":"system","subtype":"task_notification","task_id":"review","tool_use_id":"t3","status":"paused"}`)
	require.False(t, updated, "a status this platform does not know changes nothing")
	subAgents, _ = observe(`{"type":"system","subtype":"task_started","task_id":"long","tool_use_id":"t4","description":"` + strings.Repeat("任", 400) + `","task_type":"local_agent","spawn_depth":1}`)
	require.Len(t, subAgents[2].Task, 510, "a long task is cut on a character boundary")
}

// A resumed SubAgent starts again under its identity, where it already stands.
func TestAResumedSubAgentRunsAgainInPlace(t *testing.T) {
	var state nativeprocess.ProtocolState
	for _, frame := range []string{
		`{"type":"system","subtype":"task_started","task_id":"a","tool_use_id":"t1","description":"first","subagent_type":"general-purpose","spawn_depth":1,"task_type":"local_agent"}`,
		`{"type":"system","subtype":"task_notification","task_id":"a","tool_use_id":"t1","status":"completed"}`,
		`{"type":"system","subtype":"task_started","task_id":"b","tool_use_id":"t2","description":"second","subagent_type":"Explore","spawn_depth":1,"task_type":"local_agent"}`,
	} {
		var message nativeprocess.Message
		require.NoError(t, json.Unmarshal([]byte(frame), &message))
		state.ObserveSubAgents(&message)
	}
	var message nativeprocess.Message
	require.NoError(t, json.Unmarshal([]byte(`{"type":"system","subtype":"task_started","task_id":"a","tool_use_id":"t9","description":"follow up","subagent_type":"general-purpose","spawn_depth":1,"task_type":"local_agent"}`), &message))
	subAgents, updated := state.ObserveSubAgents(&message)
	require.True(t, updated)
	require.Equal(t, []command.SubAgent{{ID: "a", Type: "general-purpose", Task: "follow up", Status: "running"}, {ID: "b", Type: "Explore", Task: "second", Status: "running"}}, subAgents)
}

// A later process of the same Harness Session keeps the Plan but starts with
// no SubAgents: they ended with the process that ran them.
func TestANewProcessStartsWithoutSubAgents(t *testing.T) {
	state := nativeprocess.NewProtocolState()
	var message nativeprocess.Message
	require.NoError(t, json.Unmarshal([]byte(`{"type":"system","subtype":"task_started","task_id":"a","tool_use_id":"t1","description":"first","subagent_type":"Plan","spawn_depth":1,"task_type":"local_agent"}`), &message))
	_, updated := state.ObserveSubAgents(&message)
	require.True(t, updated)
	raw, err := nativeprocess.EncodeSnapshot(command.ConnectExecution{WorkID: "work", ProcessID: "old", NativeSessionID: "session"}, state)
	require.NoError(t, err)
	resumed, err := nativeprocess.RestoreState(command.ConnectExecution{WorkID: "work", ProcessID: "new", NativeSessionID: "session"}, raw)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(`{"type":"system","subtype":"task_notification","task_id":"a","tool_use_id":"t1","status":"completed"}`), &message))
	_, updated = resumed.ObserveSubAgents(&message)
	require.False(t, updated, "the earlier process's SubAgent is not this process's")
}
