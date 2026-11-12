package nativeprocess_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

// The Harness keeps its title in the transcript only; the Work's Stop hook
// answers it as its systemMessage, which the Harness emits as an
// informational system message after a label of its own. This is one Claude
// Code 2.1.266 emitted.
const titleMessage = `{"type":"system","subtype":"informational","content":"Stop says: talgent-title:\"Go context 包说明\"","level":"notice","uuid":"0cb72041-6aaf-45c8-8f42-465648ccdbf2","session_id":"30d347f9-fbc3-4d70-a16e-b68581f3353f"}`

func TestTheTitleIsReadFromTheStopHooksSystemMessage(t *testing.T) {
	long := strings.Repeat("长", 250)
	for raw, want := range map[string]string{
		titleMessage: "Go context 包说明",
		`{"type":"system","subtype":"informational","content":"talgent-title:\"  two\\nlines \" and more"}`: "two lines",
		`{"type":"system","subtype":"informational","content":"talgent-title:\"` + long + `\""}`:            strings.Repeat("长", 200),
		`{"type":"system","subtype":"informational","content":"Another hook says: done"}`:                   "",
		`{"type":"system","subtype":"informational","content":"talgent-title:not json"}`:                    "",
		`{"type":"system","subtype":"informational","content":{"text":"talgent-title:\"Shaped\""}}`:         "",
		`{"type":"system","subtype":"status","content":"talgent-title:\"Elsewhere\""}`:                      "",
		`{"type":"assistant","content":"talgent-title:\"Spoken\""}`:                                         "",
	} {
		var message nativeprocess.Message
		require.NoError(t, json.Unmarshal([]byte(raw), &message), raw)
		require.Equal(t, want, nativeprocess.ReportedTitle(&message), raw)
	}
}

// Each Stop repeats the title; only a new one is reported.
func TestOnlyANewTitleIsReported(t *testing.T) {
	var state nativeprocess.ProtocolState
	require.False(t, state.ObserveTitle(""))
	require.True(t, state.ObserveTitle("Go context 包说明"))
	require.False(t, state.ObserveTitle("Go context 包说明"))
	require.True(t, state.ObserveTitle("Go context cancellation"))
}
