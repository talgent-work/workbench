//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	shared "github.com/talgent-work/workbench/internal/pkg/opensandbox"
	"xorm.io/xorm"
)

func TestProvisioningPersistsSubmissionAndReconcilesUncertainOutcome(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	t.Run("continuation launch retains the Work thinking level", func(t *testing.T) {
		provider, _, _ := sandboxClient(t)
		p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		input := request()
		input.WorkID = "retained-thinking"
		input.ThinkingLevel = "high"
		require.NoError(t, p.Register(ctx, input, testMounts))
		require.NoError(t, p.Register(ctx, input, testMounts), "recovery must reuse the admitted launch")
		stored, found, err := p.live(ctx, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.Contains(t, stored.LaunchCommand, "talgent_effort='high'")
		admitted, err := decodeStartRequest(stored.RequestJSON)
		require.NoError(t, err)
		require.Equal(t, "high", admitted.ThinkingLevel)
		input.ThinkingLevel = "low"
		require.Error(t, p.Register(ctx, input, testMounts), "a repeated attempt cannot replace its launch setting")
		_, err = db.Exec("DELETE FROM execution_allocation WHERE id = ?", stored.ID)
		require.NoError(t, err)
	})

	t.Run("provider timeout preserves scheduling progress and uncertain submission", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		}))
		defer server.Close()
		provider, _, _ := sandboxClient(t)
		provider.client, err = shared.NewLifecycleClient(server.Client(), shared.Config{Address: server.URL + "/v1", APIKey: "lifecycle-secret"})
		require.NoError(t, err)
		now := time.Now()
		p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, func() time.Time { return now }, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		// A short attempt bound expires quickly. The batch outlives it by far
		// but expires before submitPending's own per-attempt bound, so only the
		// provisioning's own attempt expiry leaves the batch alive.
		p.attemptTimeout = 100 * time.Millisecond
		input := request()
		input.WorkID = "provider-timeout"
		require.NoError(t, p.Register(ctx, input, testMounts))
		before, found, err := latestAllocation(ctx, p.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		now = now.Add(time.Second)
		batch, cancel := context.WithTimeout(ctx, reconciliationAttemptTimeout/2)
		defer cancel()
		require.ErrorIs(t, submitPending(batch, p), context.DeadlineExceeded)
		require.NoError(t, batch.Err(), "per-attempt expiry must leave the batch alive for persistence and later obligations")
		after, found, err := latestAllocation(ctx, p.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, after.Submitted, "timeout does not authorize another create")
		require.Empty(t, after.SandboxID)
		require.Greater(t, after.UpdatedAt, before.UpdatedAt)
		// This isolated fixture has no provider outcome to reconcile in later cases.
		_, err = db.Exec("UPDATE execution_allocation SET deleted_at = ? WHERE session_id = ? AND deleted_at = 0", now.UnixMilli(), input.WorkID)
		require.NoError(t, err)
	})

	// Storage owns the spaces themselves; retiring an environment only retires
	// its allocation, and a later start registers a new one.
	t.Run("environment retirement retires only the allocation", func(t *testing.T) {
		provider, _, _ := sandboxClient(t)
		p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		removals := NewRemovals(db, time.Now)
		environments, err := NewEnvironments(p, removals, allocationObserverFixture(t, p, dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING))
		require.NoError(t, err)
		input := request()
		input.WorkID = "environment-retirement"
		require.NoError(t, environments.Create(ctx, input, testMounts))
		require.ErrorIs(t, environments.Delete(ctx, input.WorkID), errEnvironmentConflict)
		require.NoError(t, submitPending(ctx, p))
		// The acknowledged exit of the bound process authorizes retirement.
		_, err = db.Exec("UPDATE execution_allocation SET process_id = 'current-process', process_exited = 1 WHERE session_id = ? AND deleted_at = 0", input.WorkID)
		require.NoError(t, err)
		require.NoError(t, environments.Delete(ctx, input.WorkID))
		require.NoError(t, environments.Delete(ctx, input.WorkID))
		record, exists, err := latestAllocation(ctx, p.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, exists)
		require.NotZero(t, record.DeletedAt)
		obligations, err := pendingRemovals(ctx, removals)
		require.NoError(t, err)
		require.Len(t, obligations, 1)
		require.Equal(t, sandboxID, obligations[0].SandboxID)
		require.NoError(t, environments.Create(ctx, input, testMounts))
		next, exists, err := latestAllocation(ctx, p.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, exists)
		require.NotEqual(t, record.ID, next.ID)
		require.Zero(t, next.DeletedAt)
		// Isolate later cases from this unsubmitted allocation.
		_, err = db.Exec("UPDATE execution_allocation SET deleted_at = ? WHERE id = ?", time.Now().UnixMilli(), next.ID)
		require.NoError(t, err)
	})

	// A start abandoned before preparation never bound a process; once past
	// its Startup Deadline its environment is retired without an exit, while
	// a younger allocation still waits for one.
	t.Run("abandoned start retires without a process", func(t *testing.T) {
		provider, _, _ := sandboxClient(t)
		credentials := fixtureSandboxCredentials(db, provider)
		// Its Work reaches the run, so nothing reclaims it beyond reach.
		credentials.works = NewHeldWorks(db, &unreachedWorks{})
		p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		removals := NewRemovals(db, time.Now)
		environments, err := NewEnvironments(p, removals, allocationObserverFixture(t, p, dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING))
		require.NoError(t, err)
		input := request()
		input.WorkID = "environment-abandoned"
		require.NoError(t, environments.Create(ctx, input, testMounts))
		require.NoError(t, submitPending(ctx, p))
		require.NoError(t, environments.Delete(ctx, input.WorkID))
		young, _, err := latestAllocation(ctx, p.db, input.WorkID)
		require.NoError(t, err)
		require.Zero(t, young.DeletedAt, "within its deadline the start may still prepare, so it stays")
		_, err = db.Exec("UPDATE execution_allocation SET created_at = ? WHERE session_id = ? AND deleted_at = 0", time.Now().Add(-domain.StartupDeadline-abandonmentMargin).UnixMilli(), input.WorkID)
		require.NoError(t, err)
		require.NoError(t, environments.Delete(ctx, input.WorkID))
		record, exists, err := latestAllocation(ctx, p.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, exists)
		require.NotZero(t, record.DeletedAt)
		obligations, err := pendingRemovals(ctx, removals)
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(obligations, func(o removalRecord) bool { return o.SandboxID == sandboxID }))
	})

	t.Run("restart retains original specification and credential", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		input := request()
		input.WorkID = "restart"
		provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		require.NoError(t, provisioning.Register(ctx, input, testMounts))
		require.Zero(t, fixture.creates, "registration cannot allocate before its create is durable")
		before, found, err := latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		var spec sdk.CreateSandboxRequest
		require.NoError(t, json.Unmarshal(before.SpecificationJSON, &spec))
		require.NotEmpty(t, spec.Env["EXECD_ACCESS_TOKEN"])
		require.NoError(t, db.Close())
		db, err = xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		changed := config()
		changed.Image = "new-image:after-restart"
		nextProvider, err := NewSandboxes(provider.client, changed)
		require.NoError(t, err)
		provisioning = NewProvisioning(db, nextProvider, fixtureSandboxCredentials(db, nextProvider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		require.NoError(t, provisioning.Register(ctx, input, testMounts))
		require.NoError(t, submitPending(ctx, provisioning))
		require.Equal(t, 1, fixture.creates)
		require.Equal(t, "native-claude:test", fixture.create.Image.URI)
		require.Equal(t, spec.Env["EXECD_ACCESS_TOKEN"], fixture.create.Env["EXECD_ACCESS_TOKEN"])
		after, found, err := latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, sandboxID, after.SandboxID)
		require.True(t, after.Submitted)
		require.NoError(t, submitPending(ctx, provisioning))
		require.Equal(t, 1, fixture.creates)
		require.ErrorIs(t, provisioning.update(ctx, before, "sandbox_id = ?", "4d21dd91-f41a-443b-a77a-d303a160c29d"), errEnvironmentConflict)
		input.Capabilities.Prompt = "changed capabilities"
		require.ErrorIs(t, provisioning.Register(ctx, input, testMounts), errEnvironmentConflict)
	})

	t.Run("lost response is recovered by lookup with no second POST", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.loseCreateResponse = true
		provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		input := request()
		input.WorkID = "ambiguous"
		require.NoError(t, provisioning.Register(ctx, input, testMounts))
		require.Error(t, submitPending(ctx, provisioning))
		record, found, err := latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, record.Submitted)
		require.Empty(t, record.SandboxID)
		require.NotNil(t, fixture.info, "external allocation survived failed response")
		provisioning = NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		require.NoError(t, submitPending(ctx, provisioning))
		record, _, err = latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.Equal(t, sandboxID, record.SandboxID)
		require.Equal(t, 1, fixture.creates)
	})

	t.Run("refused create rejects the start and retires without deletion", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.refuseStatus = http.StatusBadRequest
		provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		dispatcher := NewDispatcherControl(nil, "http://unused.invalid", "unused")
		observations, err := NewProcessObservations(provisioning, dispatcher)
		require.NoError(t, err)
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { _ = root.Close() })
		resources, err := NewEnvironments(provisioning, NewRemovals(db, time.Now), observations)
		require.NoError(t, err)
		input := request()
		input.WorkID = "refused"
		require.NoError(t, provisioning.Register(ctx, input, testMounts))
		require.ErrorIs(t, submitPending(ctx, provisioning), errCreationRefused)
		record, found, err := latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, record.Submitted)
		require.True(t, record.StartRejected, "a refused create proves no compute exists")
		observation, err := observations.ObserveProcess(ctx, input.WorkID)
		require.NoError(t, err)
		require.Equal(t, domain.ProcessStartRejected, observation.State)
		require.NoError(t, submitPending(ctx, provisioning), "a rejected start is settled, never submitted again")
		environment, err := NewEnvironment(resources, dispatcher, directoryMounts{root: t.TempDir()}, cloudComputers{})
		require.NoError(t, err)
		require.NoError(t, environment.StopExecution(ctx, input.WorkID, "stop-refused"), "a rejected start has no process to stop and needs no Dispatcher")
		require.NoError(t, resources.Delete(ctx, input.WorkID))
		require.NoError(t, resources.Delete(ctx, input.WorkID), "retirement is idempotent")
		observation, err = observations.ObserveProcess(ctx, input.WorkID)
		require.NoError(t, err)
		require.True(t, observation.ComputeReclaimed, "the Work may start again")
		require.Equal(t, 1, fixture.creates)
		require.Zero(t, fixture.deletes)
	})

	t.Run("refused create keeps a sandbox the provider created anyway", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.refuseStatus, fixture.createOnRefusal = http.StatusBadRequest, true
		provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		input := request()
		input.WorkID = "refused-but-created"
		require.NoError(t, provisioning.Register(ctx, input, testMounts))
		require.NoError(t, submitPending(ctx, provisioning))
		record, found, err := latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, record.StartRejected, "existing compute is never retired as a rejected start")
		require.Equal(t, sandboxID, record.SandboxID)
		require.Equal(t, 1, fixture.creates)
	})

	t.Run("concurrent workers cannot double submit", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		input := request()
		input.WorkID = "concurrent"
		require.NoError(t, provisioning.Register(ctx, input, testMounts))
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Add(1)
			// Separate workers retain the cross-process CAS race even though each
			// worker serializes its own scan cursor.
			worker := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
			go func() { defer workers.Done(); <-start; results <- submitPending(ctx, worker) }()
		}
		close(start)
		workers.Wait()
		close(results)
		for err := range results {
			require.True(t, err == nil || errors.Is(err, errEnvironmentConflict), "unexpected worker error: %v", err)
		}
		require.NoError(t, submitPending(ctx, provisioning))
		require.Equal(t, 1, fixture.creates)
		record, _, err := latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.Equal(t, sandboxID, record.SandboxID)
	})

	t.Run("empty lookup cannot authorize blind resubmission", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
		input := request()
		input.WorkID = "uncertain-no-observation"
		require.NoError(t, provisioning.Register(ctx, input, testMounts))
		record, _, err := latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		// Crash after the durable claim but before POST is indistinguishable from
		// an external request still in flight; absence is not definitive failure.
		require.NoError(t, provisioning.update(ctx, record, "submitted = 1"))
		claimed, _, err := latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		provisioning.now = func() time.Time { return time.UnixMilli(claimed.UpdatedAt + 1000) }
		require.NoError(t, submitPending(ctx, provisioning))
		require.NoError(t, submitPending(ctx, provisioning))
		require.Zero(t, fixture.creates)
		record, _, err = latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.Empty(t, record.SandboxID)
		require.True(t, record.Submitted)
		require.Equal(t, claimed.UpdatedAt, record.UpdatedAt, "empty lookup must not rewrite the allocation row")
		require.Equal(t, claimed.Version, record.Version)
		// A late external observation still discharges the same create.
		var spec sdk.CreateSandboxRequest
		require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
		fixture.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: spec.Metadata}
		require.NoError(t, submitPending(ctx, provisioning))
		record, _, err = latestAllocation(ctx, provisioning.db, input.WorkID)
		require.NoError(t, err)
		require.Equal(t, sandboxID, record.SandboxID)
		require.Zero(t, fixture.creates)
	})
}
