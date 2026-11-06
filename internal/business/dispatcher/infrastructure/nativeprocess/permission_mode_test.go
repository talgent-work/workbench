package nativeprocess_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

// Claude Code reports its permission mode in its init message and in a
// status message whenever the mode changes, also by itself; nothing else
// names one.
func TestThePermissionModeIsReadFromInitAndStatusMessages(t *testing.T) {
	for raw, want := range map[string]string{
		`{"type":"system","subtype":"init","permissionMode":"bypassPermissions"}`:           "bypassPermissions",
		`{"type":"system","subtype":"status","status":null,"permissionMode":"acceptEdits"}`: "acceptEdits",
		`{"type":"system","subtype":"status","status":"compacting"}`:                        "",
		`{"type":"assistant","permissionMode":"plan"}`:                                      "",
	} {
		var message nativeprocess.Message
		require.NoError(t, json.Unmarshal([]byte(raw), &message))
		require.Equal(t, want, nativeprocess.ReportedPermissionMode(&message), raw)
	}
}

// Only a change of a known mode is reported; a later Harness's unknown mode
// keeps the last known one.
func TestOnlyAChangedKnownPermissionModeIsReported(t *testing.T) {
	state := &nativeprocess.ProtocolState{}
	changed, known := state.ObservePermissionMode("plan")
	require.True(t, changed && known)
	changed, known = state.ObservePermissionMode("plan")
	require.False(t, changed)
	require.True(t, known)
	changed, known = state.ObservePermissionMode("someFutureMode")
	require.False(t, changed || known)
	require.Equal(t, "plan", state.PermissionMode)
	for _, mode := range []string{"default", "acceptEdits", "bypassPermissions", "dontAsk", "auto"} {
		changed, _ = state.ObservePermissionMode(mode)
		require.True(t, changed, mode)
	}
}
