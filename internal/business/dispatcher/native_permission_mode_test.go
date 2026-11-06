package dispatcher

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

// The Harness's answer accepting a set_permission_mode request reports the
// mode as a status message would, even after its waiter gave up; a refusal
// or another control answer reports none.
func TestAnAcceptedPermissionModeAnswerReportsTheMode(t *testing.T) {
	answer := func(id, subtype, mode string) *nativeprocess.Message {
		message := &nativeprocess.Message{Type: "control_response"}
		message.Response.RequestID, message.Response.Subtype, message.Response.Response.Mode = id, subtype, mode
		return message
	}
	require.Equal(t, "plan", reportedPermissionMode(answer(permissionRequestPrefix+"1", "success", "plan")))
	require.Empty(t, reportedPermissionMode(answer(permissionRequestPrefix+"1", "error", "plan")))
	require.Empty(t, reportedPermissionMode(answer(thinkingRequestPrefix+"1", "success", "plan")))
	require.Equal(t, "auto", reportedPermissionMode(&nativeprocess.Message{Type: "system", Subtype: "status", PermissionMode: "auto"}))
}
