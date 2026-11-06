//go:build integration

package dispatcher_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

// The same native Harness must survive a model rejection and execute the next
// accepted input. Product error history alone cannot prove the Work lifecycle.
func assertNativeErrorAllowsNextInput(t *testing.T, model *nativeInference, records *nativeRecords, get func() *agentprivatev1.GetWorkResponse, send func(string) error) {
	t.Helper()
	model.mu.Lock()
	model.apiFailure = true
	model.mu.Unlock()
	records.mu.Lock()
	errorStart := len(records.messages)
	records.mu.Unlock()
	require.NoError(t, send("model rejection through Agent API"))
	records.awaitResult(t, "model rejection through Agent API")
	records.mu.Lock()
	sawFailedResult := false
	for _, message := range records.messages[errorStart:] {
		if result := message.GetResult(); result != nil && result.Status == dispatcherpublicv1.ResultStatus_RESULT_STATUS_FAILED && strings.Contains(result.Text, "fixture API credential rejected") {
			sawFailedResult = true
		}
	}
	records.mu.Unlock()
	require.True(t, sawFailedResult, "the rejected Turn must end unsuccessfully with the provider's explanation")
	require.Eventually(t, func() bool {
		view := get()
		return view.State == agentprivatev1.WorkState_WORK_STATE_WORKING && !view.Waiting && view.FailureReason == ""
	}, 5*time.Second, 20*time.Millisecond)
	model.mu.Lock()
	model.apiFailure = false
	model.mu.Unlock()
	records.mu.Lock()
	nextStart := len(records.messages)
	records.mu.Unlock()
	require.NoError(t, send("continue after model rejection"))
	records.awaitResult(t, "continue after model rejection")
	records.mu.Lock()
	sawSuccess := false
	for _, message := range records.messages[nextStart:] {
		if result := message.GetResult(); result != nil && result.Status == dispatcherpublicv1.ResultStatus_RESULT_STATUS_SUCCESS {
			sawSuccess = true
		}
	}
	records.mu.Unlock()
	require.True(t, sawSuccess, "the same Harness must complete the next input successfully")
	require.Eventually(t, func() bool {
		view := get()
		return view.State == agentprivatev1.WorkState_WORK_STATE_WORKING && !view.Waiting && view.FailureReason == ""
	}, 5*time.Second, 20*time.Millisecond)
}
