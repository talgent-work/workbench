package infrastructure

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

func TestStartupRecoveryUsesTheUnfinishedBoundary(t *testing.T) {
	initial := command.InitialExecutionRequest{PrepareWorkExecution: command.PrepareWorkExecution{WorkID: "session", RequestID: "initial"}, Submitted: true}
	encode := func(v any) string { t.Helper(); b, err := json.Marshal(v); require.NoError(t, err); return string(b) }
	row := workViewRow{ID: "session", State: 2, Version: 3, InitialID: "initial", InitialData: encode(initial), InitialVersion: 4, StartRequestID: "initial", HarnessProcess: encode(harnessProcessRecord{Prepared: true, Started: true})}
	progress, err := startupFromRows(row, true, true, 9)
	require.NoError(t, err)
	require.True(t, progress.Admitted)
	require.Equal(t, "preparing", progress.Status, "a reconnect error does not prove workspace preparation is recovering")
	require.Equal(t, "workspace", progress.Stages[2].Stage, "checkout precedes the agent")
	require.Equal(t, "active", progress.Stages[2].Status)
	initial.RecoveryPending = true
	row.InitialData = encode(initial)
	row.InitialVersion++
	recovered, err := startupFromRows(row, true, false, 10)
	require.NoError(t, err)
	require.Equal(t, "recovering", recovered.Status)
	require.Greater(t, recovered.Revision, progress.Revision)
	// A later start's readiness must not inherit either earlier environment
	// readiness or the completed stages of the previous startup.
	initial.InputPrepared = true
	initial.InputBuffered = true
	initial.RecoveryPending = false
	row.InitialData = encode(initial)
	row.Version++
	row.State = 1
	row.StartRequestID = "resume"
	row.HarnessProcess = encode(harnessProcessRecord{})
	resumed, err := startupFromRows(row, false, false, 1)
	require.NoError(t, err)
	require.Equal(t, "resume", resumed.RequestID)
	require.Len(t, resumed.Stages, 3)
	require.Equal(t, "active", resumed.Stages[1].Status)
	require.Greater(t, resumed.Revision, recovered.Revision, "a new start may reset its allocation version")
}

// A Restart that reaches its Startup Deadline shows the Work's own failure on
// the failed stage, while its incomplete first start still shows workspace
// and task stages.
func TestStartupDeadlineFailureOfARestartShowsTheWorkFailure(t *testing.T) {
	initial := command.InitialExecutionRequest{PrepareWorkExecution: command.PrepareWorkExecution{WorkID: "session", RequestID: "initial"}, Submitted: true}
	encode := func(v any) string { t.Helper(); b, err := json.Marshal(v); require.NoError(t, err); return string(b) }
	reason := "Startup Deadline reached before the execution environment was ready."
	row := workViewRow{ID: "session", State: 5, Version: 7, InitialID: "initial", InitialData: encode(initial), InitialVersion: 2, StartRequestID: "restart", Failure: reason, HarnessProcess: encode(harnessProcessRecord{})}
	progress, err := startupFromRows(row, false, false, 1)
	require.NoError(t, err)
	require.Equal(t, "failed", progress.Status)
	require.Equal(t, reason, progress.Reason)
	require.Equal(t, []query.StartupStage{{Stage: "admission", Status: "completed"}, {Stage: "sandbox", Status: "failed"}, {Stage: "workspace", Status: "pending"}, {Stage: "agent", Status: "pending"}, {Stage: "input", Status: "pending"}}, progress.Stages)
}

// A Resume its Computer refused as unreachable leaves the earlier start's
// facts shown, while any other refusal of it shows as the startup it ended.
func TestAResumeRefusedAsUnreachableShowsNoFailedStartup(t *testing.T) {
	encode := func(v any) string { t.Helper(); b, err := json.Marshal(v); require.NoError(t, err); return string(b) }
	row := workViewRow{ID: "session", State: 4, Version: 5, StartRequestID: "first", HarnessProcess: encode(harnessProcessRecord{Prepared: true, Started: true}), ContinuationID: "resume", ContinuationVersion: 2, ContinuationRejection: "unreachable"}
	progress, err := startupFromRows(row, true, false, 1)
	require.NoError(t, err)
	require.Equal(t, "first", progress.RequestID)
	require.Equal(t, "completed", progress.Status)
	require.Empty(t, progress.Reason)

	row.ContinuationRejection = "capacity"
	refused, err := startupFromRows(row, true, false, 1)
	require.NoError(t, err)
	require.Equal(t, "resume", refused.RequestID)
	require.Equal(t, "failed", refused.Status)
	require.Equal(t, "capacity", refused.Reason)
}
