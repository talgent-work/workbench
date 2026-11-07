package execution

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// serveHeldCreate creates the sandbox, then answers only once answer is
// released, without holding the fixture meanwhile, so the sandbox is listed
// and found while its create waits. It is called holding the fixture.
func (f *lifecycleFixture) serveHeldCreate(w http.ResponseWriter, r *http.Request) {
	f.requests++
	f.creates++
	if r.Header.Get("OPEN-SANDBOX-API-KEY") != "lifecycle-secret" || json.NewDecoder(r.Body).Decode(&f.create) != nil {
		f.mu.Unlock()
		w.WriteHeader(400)
		return
	}
	f.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: maps.Clone(f.create.Metadata), Status: sdk.SandboxStatus{State: sdk.StateRunning}}
	if f.createPending {
		f.info.Status.State = "Pending"
	}
	answer := f.answer
	f.mu.Unlock()
	if answer != nil {
		select {
		case <-answer:
		case <-r.Context().Done():
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.failAfterCreate != 0 {
		f.info = nil
		f.refuse(w, f.failAfterCreate)
		return
	}
	_ = json.NewEncoder(w).Encode(f.info)
}

// refuse answers a create with status, carrying refuseCode when set.
func (f *lifecycleFixture) refuse(w http.ResponseWriter, status int) {
	if f.refuseCode == "" {
		http.Error(w, "invalid parameters", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(sdk.ErrorResponse{Code: f.refuseCode, Message: "invalid parameters"})
}

// A create is definitely refused or failed only when OpenSandbox says so: a
// refused or unschedulable specification, or a failure after which it
// deleted the sandbox (its readiness timeout, a failed Pod, an exhausted
// quota). Any other failure leaves the outcome unknown.
func TestSandboxCreateRefusalIsOnlyADefiniteAnswer(t *testing.T) {
	for _, tc := range []struct {
		status  int
		code    string
		refused bool
	}{
		{status: http.StatusBadRequest, refused: true},
		{status: http.StatusBadRequest, code: "SANDBOX::INVALID_PARAMETER", refused: true},
		{status: http.StatusUnprocessableEntity, refused: true},
		{status: http.StatusGatewayTimeout, code: "KUBERNETES::POD_READY_TIMEOUT", refused: true},
		{status: http.StatusInternalServerError, code: "KUBERNETES::POD_FAILED", refused: true},
		{status: http.StatusForbidden, code: "KUBERNETES::QUOTA_EXCEEDED", refused: true},
		{status: http.StatusGatewayTimeout},
		{status: http.StatusInternalServerError},
		{status: http.StatusInternalServerError, code: "KUBERNETES::API_ERROR"},
		{status: http.StatusServiceUnavailable},
		{status: http.StatusConflict},
	} {
		provider, _, fixture := sandboxClient(t)
		fixture.refuseStatus, fixture.refuseCode = tc.status, tc.code
		_, err := provider.submitRequest(context.Background(), request(), "execd-secret")
		require.Error(t, err)
		require.Equal(t, tc.refused, errors.Is(err, errCreationRefused), "HTTP %d %s", tc.status, tc.code)
		require.NotContains(t, err.Error(), "invalid parameters", "response bodies never leave the adapter")
	}
}

// A create waits for its answer a little longer than the OpenSandbox
// server's creation timeout, its default unless configured; the timeout must
// leave room within the Startup Deadline.
func TestSandboxCreateWaitsLongerThanTheServerCreationTimeout(t *testing.T) {
	require.Equal(t, 5*time.Minute+30*time.Second, config().creationWait())
	configured := config()
	configured.CreationTimeout = 2 * time.Minute
	require.Equal(t, 2*time.Minute+30*time.Second, configured.creationWait())
	require.NoError(t, configured.Validate())
	for _, invalid := range []time.Duration{-time.Second, domain.StartupDeadline - 30*time.Second} {
		configured.CreationTimeout = invalid
		require.Error(t, configured.Validate(), invalid)
	}
}

// A create never answered is bounded by its caller and sent once.
func TestSandboxCreateNeverAnsweredIsSentOnce(t *testing.T) {
	provider, _, fixture := sandboxClient(t)
	fixture.answer = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := provider.submitRequest(ctx, request(), "execd-secret")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, errors.Is(err, errCreationRefused), "an unanswered create is no refusal")
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	require.Equal(t, 1, fixture.creates)
	require.NotNil(t, fixture.info, "the sandbox it created remains")
}
