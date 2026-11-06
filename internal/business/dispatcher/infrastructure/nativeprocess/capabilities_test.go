package nativeprocess_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

func TestCapabilitiesReplaceOnlyCompleteRootInventories(t *testing.T) {
	state := nativeprocess.ProtocolState{}
	observe := func(raw string) bool {
		var message nativeprocess.Message
		require.NoError(t, json.Unmarshal([]byte(raw), &message))
		return state.ObserveCapabilities(&message, "init")
	}
	require.False(t, state.Capabilities.Observed)
	require.True(t, observe(`{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{"commands":[{"name":"compact","description":"Compact","argumentHint":"[instructions]"},{"name":"plugin:check","argumentHint":"[path]"}]}}}`))
	require.Equal(t, application.Capabilities{Observed: true, Commands: []application.NativeCommand{{Name: "compact", Description: "Compact", ArgumentHint: "[instructions]"}, {Name: "plugin:check", ArgumentHint: "[path]"}}}, state.Capabilities)
	before := state.Capabilities
	for _, raw := range []string{
		`{"type":"system","subtype":"commands_changed"}`,
		`{"type":"system","subtype":"commands_changed","commands":null}`,
		`{"type":"system","subtype":"commands_changed","commands":{}}`,
		`{"type":"system","subtype":"commands_changed","commands":[{"name":"good"},{}]}`,
		`{"type":"system","subtype":"commands_changed","commands":[{"name":"x"},{"name":"x"}]}`,
		`{"type":"system","subtype":"commands_changed","commands":[],"parent_tool_use_id":"child"}`,
		`{"type":"system","subtype":"commands_changed","commands":[],"agent_id":"child"}`,
		`{"type":"system","subtype":"init","commands":[]}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"other","response":{"commands":[]}}}`,
		`{"type":"control_response","response":{"subtype":"error","request_id":"init","response":{"commands":[]}}}`,
	} {
		require.False(t, observe(raw), raw)
		require.Equal(t, before, state.Capabilities, "malformed and unrelated reports cannot erase candidates")
	}
	require.True(t, observe(`{"type":"system","subtype":"commands_changed","commands":[{"name":"replacement","description":"New","argumentHint":"[value]"}]}`))
	require.Equal(t, []application.NativeCommand{{Name: "replacement", Description: "New", ArgumentHint: "[value]"}}, state.Capabilities.Commands)
	require.True(t, observe(`{"type":"system","subtype":"commands_changed","commands":[]}`))
	require.True(t, state.Capabilities.Observed)
	require.Empty(t, state.Capabilities.Commands)
}
