package nativeprocess_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

func TestNativeTaskResultsProduceCompleteWorkSnapshotsAcrossCheckpointReload(t *testing.T) {
	// Captured from Claude Code 2.1.266 with deterministic local inference.
	// Failed TodoWrite is present because this task-enabled Harness disables it.
	raw, err := os.ReadFile("testdata/work_progress_claude_2_1_266.json")
	require.NoError(t, err)
	var frames []nativeprocess.Message
	require.NoError(t, json.Unmarshal(raw, &frames))
	var state nativeprocess.ProtocolState
	var snapshots [][]command.WorkProgressItem
	for _, frame := range frames {
		items, updated := state.ObserveWorkProgress(&frame)
		if updated {
			require.Equal(t, "user", frame.Type, "a tool request does not establish its result")
			snapshots = append(snapshots, items)
		}
		checkpoint, err := json.Marshal(state)
		require.NoError(t, err)
		state = nativeprocess.ProtocolState{}
		require.NoError(t, json.Unmarshal(checkpoint, &state))
	}
	require.Len(t, snapshots, 7)
	require.Equal(t, []command.WorkProgressItem{{Content: "Inspect source", Status: "pending", ActiveForm: "Inspecting source"}}, snapshots[0])
	for _, snapshot := range snapshots[1:4] {
		require.Equal(t, []command.WorkProgressItem{{Content: "Inspect source", Status: "in_progress", ActiveForm: "Inspecting source"}}, snapshot)
	}
	require.Equal(t, "completed", snapshots[4][0].Status)
	require.Empty(t, snapshots[5])
	require.Empty(t, snapshots[6])
}

// A retained snapshot belongs to a Harness Session, not every later process
// of the Work. TaskUpdate supplies only a delta, so it needs the prior task.
func TestResumedTaskUpdatesInheritOnlyTheSameHarnessSession(t *testing.T) {
	state := nativeprocess.NewProtocolState()
	for _, frame := range taskFrames(t, "create", "TaskCreate", `{"subject":"Inspect source"}`, `{"task":{"id":"1","subject":"Inspect source"}}`) {
		state.ObserveWorkProgress(&frame)
	}
	state.Exited, state.Offset = true, 1234
	binding := command.ConnectExecution{WorkID: "work", ProcessID: "old-process", NativeSessionID: "session"}
	raw, err := nativeprocess.EncodeSnapshot(binding, state)
	require.NoError(t, err)
	for _, session := range []string{"session", "unrelated-session"} {
		t.Run(session, func(t *testing.T) {
			resumed, err := nativeprocess.RestoreState(command.ConnectExecution{WorkID: "work", ProcessID: "new-process", NativeSessionID: session}, raw)
			require.NoError(t, err)
			require.False(t, resumed.Exited, "a new process must not inherit the old exit")
			require.Zero(t, resumed.Offset, "a new stream starts at its own beginning")
			var items []command.WorkProgressItem
			var updated bool
			for _, frame := range taskFrames(t, "complete", "TaskUpdate", `{"taskId":"1","status":"completed"}`, `{"success":true,"taskId":"1","updatedFields":["status"],"statusChange":{"to":"completed"}}`) {
				items, updated = resumed.ObserveWorkProgress(&frame)
			}
			if session == "session" {
				require.True(t, updated)
				require.Equal(t, []command.WorkProgressItem{{Content: "Inspect source", Status: "completed"}}, items)
			} else {
				require.False(t, updated, "another Session's local task 1 cannot update this plan")
				require.Empty(t, items)
			}
		})
	}
}

func TestNativeTodoResultUsesTheAppliedPlanAndExcludesSubagents(t *testing.T) {
	raw, err := os.ReadFile("testdata/todo_progress_claude_2_1_266.json")
	require.NoError(t, err)
	var frames []nativeprocess.Message
	require.NoError(t, json.Unmarshal(raw, &frames))
	require.Len(t, frames, 2)
	var state nativeprocess.ProtocolState
	for _, owner := range []struct{ parent, agent string }{{parent: "delegation-1"}, {agent: "native-member"}} {
		for _, frame := range frames {
			child := frame
			child.ParentToolUseID, child.AgentID = owner.parent, owner.agent
			_, updated := state.ObserveWorkProgress(&child)
			require.False(t, updated, "a delegated Agent or team member cannot replace the root plan")
		}
	}
	_, updated := state.ObserveWorkProgress(&frames[0])
	require.False(t, updated)
	frames[1].ToolUseResult = json.RawMessage(`{"oldTodos":[],"newTodos":[{"content":"Applied task","status":"completed"}]}`)
	items, updated := state.ObserveWorkProgress(&frames[1])
	require.True(t, updated)
	require.Equal(t, []command.WorkProgressItem{{Content: "Applied task", Status: "completed"}}, items)
}

func TestTaskUpdateUsesConfirmedFieldsInsteadOfRequestedStatus(t *testing.T) {
	raw, err := os.ReadFile("testdata/work_progress_claude_2_1_266.json")
	require.NoError(t, err)
	var frames []nativeprocess.Message
	require.NoError(t, json.Unmarshal(raw, &frames))
	var state nativeprocess.ProtocolState
	for _, frame := range frames[:3] {
		state.ObserveWorkProgress(&frame)
	}
	frames[3].ToolUseResult = json.RawMessage(`{"success":true,"taskId":"1","updatedFields":["status"],"statusChange":{"from":"pending","to":"completed"}}`)
	items, updated := state.ObserveWorkProgress(&frames[3])
	require.True(t, updated)
	require.Equal(t, "completed", items[0].Status)
}

// taskFrames is one native task call: its request and its applied result.
func taskFrames(t *testing.T, id, name, input, result string) []nativeprocess.Message {
	t.Helper()
	var request, response nativeprocess.Message
	require.NoError(t, json.Unmarshal([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"`+id+`","name":"`+name+`","input":`+input+`}]}}`), &request))
	require.NoError(t, json.Unmarshal([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"`+id+`"}]},"tool_use_result":`+result+`}`), &response))
	return []nativeprocess.Message{request, response}
}

func taskContents(items []command.WorkProgressItem) []string {
	contents := make([]string, len(items))
	for i, item := range items {
		contents[i] = item.Content + ":" + item.Status
	}
	return contents
}

// Intent (real): Claude's task list keeps every task of the session, yet Work
// Progress shows the current batch: tasks created after every earlier task was
// completed replace them (POK-4: a later instruction added two checks after a
// finished research batch), while tasks added mid-batch extend it.
func TestWorkProgressShowsTheCurrentTaskBatch(t *testing.T) {
	var state nativeprocess.ProtocolState
	var items []command.WorkProgressItem
	observe := func(frames []nativeprocess.Message) {
		for i := range frames {
			if got, updated := state.ObserveWorkProgress(&frames[i]); updated {
				items = got
			}
		}
		checkpoint, err := json.Marshal(state)
		require.NoError(t, err)
		state = nativeprocess.ProtocolState{}
		require.NoError(t, json.Unmarshal(checkpoint, &state))
	}
	create := func(id, subject string) {
		observe(taskFrames(t, "create-"+id, "TaskCreate", `{"subject":"`+subject+`"}`, `{"task":{"id":"`+id+`","subject":"`+subject+`"}}`))
	}
	complete := func(id string) {
		observe(taskFrames(t, "update-"+id, "TaskUpdate", `{"taskId":"`+id+`","status":"completed"}`, `{"success":true,"taskId":"`+id+`","updatedFields":["status"],"statusChange":{"to":"completed"}}`))
	}

	create("1", "Research")
	create("2", "Write report")
	complete("1")
	create("3", "Cite sources")
	require.Equal(t, []string{"Research:completed", "Write report:pending", "Cite sources:pending"}, taskContents(items), "a task added mid-batch extends it")

	complete("2")
	complete("3")
	create("4", "Verify claims")
	require.Equal(t, []string{"Verify claims:pending"}, taskContents(items), "a completed batch is replaced by the next one")

	observe(taskFrames(t, "list", "TaskList", `{}`, `{"tasks":[{"id":"1","subject":"Research","status":"completed"},{"id":"2","subject":"Write report","status":"completed"},{"id":"3","subject":"Cite sources","status":"completed"},{"id":"4","subject":"Verify claims","status":"in_progress"}]}`))
	require.Equal(t, []string{"Verify claims:in_progress"}, taskContents(items), "the full native list does not bring a finished batch back")

	observe(taskFrames(t, "reopen", "TaskUpdate", `{"taskId":"2","status":"in_progress"}`, `{"success":true,"taskId":"2","updatedFields":["status"],"statusChange":{"to":"in_progress"}}`))
	require.Equal(t, []string{"Verify claims:in_progress"}, taskContents(items), "reopening a finished task keeps the current batch")

	observe(taskFrames(t, "delete", "TaskUpdate", `{"taskId":"4"}`, `{"success":true,"taskId":"4","updatedFields":["deleted"]}`))
	require.Empty(t, items, "deleting the current batch leaves nothing to show")
}
