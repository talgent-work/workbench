package query

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartupProgressFollowsCommittedFacts(t *testing.T) {
	f := StartupFacts{RequestID: "request", Revision: "1", Initial: true, State: "created"}
	assertStage := func(stage string) {
		t.Helper()
		p := ProjectStartup(f)
		require.Equal(t, "preparing", p.Status)
		for _, s := range p.Stages {
			if s.Status == "active" {
				require.Equal(t, stage, s.Stage)
				return
			}
		}
		t.Fatal("missing active stage")
	}
	assertStage("admission")
	f.Admitted = true
	f.State = "starting"
	assertStage("sandbox")
	f.SandboxReady = true
	// The environment's preparation checks repositories out before the Harness.
	assertStage("workspace")
	f.InputPrepared = true
	assertStage("agent")
	f.AgentReady = true
	f.State = "working"
	assertStage("input")
	f.InputBuffered = true
	require.Equal(t, "completed", ProjectStartup(f).Status)
	// A later normal stop does not turn a successfully finished startup into failure.
	f.State = "stopped"
	require.Equal(t, "completed", ProjectStartup(f).Status)
}

func TestStartupProgressResumeFailureAndRecovery(t *testing.T) {
	f := StartupFacts{RequestID: "resume", Revision: "2", Admitted: true, State: "starting", InputPrepared: true, InputBuffered: true}
	require.Len(t, ProjectStartup(f).Stages, 3)
	f.SandboxReady = true
	f.AgentReady = true
	f.State = "working"
	require.Equal(t, "completed", ProjectStartup(f).Status)
	// Interrupted initial input still belongs to this conversation and must finish.
	f.InputBuffered = false
	f.InputPending = true
	f.InputPrepared = false
	require.Equal(t, "workspace", ProjectStartup(f).Stages[2].Stage)
	f.Recovering = true
	require.Equal(t, "recovering", ProjectStartup(f).Status)
	f.Rejected = true
	f.Reason = "workspace preparation rejected"
	p := ProjectStartup(f)
	require.Equal(t, "failed", p.Status)
	require.Equal(t, "failed", p.Stages[2].Status)
	f.Rejected = false
	f.State = "stopping"
	require.Equal(t, "cancelled", ProjectStartup(f).Status)
}
