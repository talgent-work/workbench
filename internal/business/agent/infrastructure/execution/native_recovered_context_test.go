package execution

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A restart continues the Work's conversation when its transcript exists in
// the Work's HOME, whatever the transcript holds, and starts it afresh when
// it does not.
func TestARestartContinuesTheConversationWhoseTranscriptExists(t *testing.T) {
	input := request()
	input.ContextMode = domain.ContextRecover
	sessionID := nativeSessionID(input.WorkID, input.HarnessSessionReference)

	args, _ := launchArgs(t, input, "--resume")
	require.Equal(t, []string{"--resume", sessionID}, contextArgs(args))
	args, _ = launchArgs(t, input, "--session-id")
	require.Equal(t, []string{"--session-id", sessionID}, contextArgs(args))
}

// Without the sandbox's transcript location a restart starts nothing rather
// than guessing.
func TestARestartNeedsTheTranscriptLocation(t *testing.T) {
	input := request()
	input.ContextMode = domain.ContextRecover
	args, _ := launchArgs(t, input, "--session-id", "CLAUDE_CODE_PROJECT_DIR_NAME=")
	require.Nil(t, args)
}
