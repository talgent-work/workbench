//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	shared "github.com/talgent-work/workbench/internal/pkg/opensandbox"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// Cases whose sandboxes the fixture creates with createPending bind them
// while they start, so their reconciliation delivers nothing: the fixture's
// credentials deliver to no Vault.

// reconcilerOf reconciles executions through p, on p's clock.
func reconcilerOf(t *testing.T, db *xorm.Engine, p *Provisioning) *executionSteps {
	t.Helper()
	connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture")
	require.NoError(t, err)
	return newExecutionSteps(t, p, connections, unread{}, nil, p.now)
}

func TestProvisioningPersistsSubmissionAndReconcilesUncertainOutcome(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	newProvisioning := func(provider *Sandboxes, now func() time.Time) *Provisioning {
		return NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, now, nil)
	}
	reconcile := func(steps *executionSteps, id string) error {
		return steps.reconcile(ctx, id)
	}
	beginAgain := func(launch domain.LaunchSpecification) error {
		run, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: domain.WorkStartAdmitted{WorkID: launch.WorkID, StartAdmittedAt: time.Now(), DeskID: "desk-again"}, Launch: launch, Realization: domain.RealizationCloud})
		require.NoError(t, err)
		return NewAgentExecutionRepository(db, time.Now).Save(ctx, run)
	}

	t.Run("continuation launch retains the Work thinking level", func(t *testing.T) {
		provider, _, _ := sandboxClient(t)
		p := newProvisioning(provider, time.Now)
		input := request()
		input.WorkID = "retained-thinking"
		input.ThinkingLevel = "high"
		stored, err := register(ctx, p, input, testMounts)
		require.NoError(t, err)
		require.NoError(t, p.specify(ctx, stored, testMounts), "a repeated specification keeps the admitted launch")
		again, _, err := p.live(ctx, input.WorkID)
		require.NoError(t, err)
		require.Equal(t, stored.LaunchCommand, again.LaunchCommand)
		require.Contains(t, stored.LaunchCommand, "talgent_effort='high'")
		admitted, err := decodeStartRequest(stored.RequestJSON)
		require.NoError(t, err)
		require.Equal(t, "high", admitted.ThinkingLevel)
		input.ThinkingLevel = "low"
		require.ErrorIs(t, beginAgain(input), domain.ErrExecutionAdmission, "a repeated attempt cannot replace its launch setting")
	})

	t.Run("an unanswered create preserves the uncertain submission", func(t *testing.T) {
		// The provider shows no sandbox and never answers the create.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"items":[]}`))
				return
			}
			<-r.Context().Done()
		}))
		defer server.Close()
		provider, _, _ := sandboxClient(t)
		provider.client, err = shared.NewLifecycleClient(server.Client(), shared.Config{Address: server.URL + "/v1", APIKey: "lifecycle-secret"})
		require.NoError(t, err)
		var mu sync.Mutex
		now := time.Now()
		clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
		p := newProvisioning(provider, clock)
		steps := reconcilerOf(t, db, p)
		input := request()
		input.WorkID = "provider-timeout"
		before, err := register(ctx, p, input, testMounts)
		require.NoError(t, err)
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
		long, err := steps.handler.Reconcile(ctx, before.ID)
		require.NoError(t, err, "a reconciliation never waits for the create")
		require.True(t, long)
		waiting, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		require.ErrorIs(t, steps.handler.RunLongStep(waiting, before.ID), context.DeadlineExceeded)
		after, found, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, after.Submitted, "an unanswered create does not authorize another")
		require.Empty(t, after.SandboxID)
		require.Greater(t, after.UpdatedAt, before.UpdatedAt)
	})

	// Storage owns the spaces themselves; retiring an execution only retires
	// its record, and a later start begins a new one.
	t.Run("environment retirement retires only the allocation", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.createPending = true
		p := newProvisioning(provider, time.Now)
		steps := reconcilerOf(t, db, p)
		input := request()
		input.WorkID = "environment-retirement"
		begun, err := register(ctx, p, input, testMounts)
		require.NoError(t, err)
		require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByExit, time.Now()), "a start without a sandbox identity is no conflict")
		unbound, _, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.Zero(t, unbound.DeletedAt, "a start that has not ended is not retired")
		require.NoError(t, reconcile(steps, begun.ID))
		// The acknowledged exit of the bound process authorizes retirement.
		run := current(t, steps, begun.ID)
		_, err = run.RecordLaunch("current-process")
		require.NoError(t, err)
		run.RecordExit("current-process", domain.HarnessExit{})
		require.NoError(t, steps.executions.Save(ctx, run))
		require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByExit, time.Now()))
		require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByExit, time.Now()))
		record, exists, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.True(t, exists)
		require.NotZero(t, record.DeletedAt)
		require.Zero(t, record.ReleasedAt, "its release is outstanding")
		require.Equal(t, sandboxID, record.SandboxID)
		next, err := register(ctx, p, input, testMounts)
		require.NoError(t, err)
		require.NotEqual(t, record.ID, next.ID)
		require.Zero(t, next.DeletedAt)
	})

	// A start that never launched a process is abandoned once past its
	// Startup Deadline with its margin and retired without an exit, while a
	// younger one still waits.
	t.Run("abandoned start retires without a process", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.createPending = true
		p := newProvisioning(provider, time.Now)
		steps := reconcilerOf(t, db, p)
		input := request()
		input.WorkID = "environment-abandoned"
		begun, err := register(ctx, p, input, testMounts)
		require.NoError(t, err)
		require.NoError(t, reconcile(steps, begun.ID))
		require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByAbandonment, time.Now()))
		young, _, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.Zero(t, young.DeletedAt, "within its deadline the start may still prepare, so it stays")
		_, err = db.Exec("UPDATE agent_execution SET seated_at = ? WHERE id = ?", time.Now().Add(-domain.StartupDeadline-2*time.Minute).UnixMilli(), begun.ID)
		require.NoError(t, err)
		require.NoError(t, reconcile(steps, begun.ID))
		require.True(t, current(t, steps, begun.ID).Outcome.Abandoned)
		require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByAbandonment, time.Now()))
		record, exists, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.True(t, exists)
		require.NotZero(t, record.DeletedAt)
		outstanding, err := unreleased(ctx, db)
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(outstanding, func(o executionRecord) bool { return o.ID == record.ID && o.SandboxID == sandboxID }))
	})

	t.Run("restart retains original specification and credential", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.createPending = true
		input := request()
		input.WorkID = "restart"
		provisioning := newProvisioning(provider, time.Now)
		before, err := register(ctx, provisioning, input, testMounts)
		require.NoError(t, err)
		require.Zero(t, fixture.creates, "the specification cannot allocate before its create is durable")
		stale := current(t, reconcilerOf(t, db, provisioning), before.ID)
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
		provisioning = newProvisioning(nextProvider, time.Now)
		steps := reconcilerOf(t, db, provisioning)
		require.NoError(t, reconcile(steps, before.ID))
		require.Equal(t, 1, fixture.creates)
		require.Equal(t, "native-claude:test", fixture.create.Image.URI)
		require.Equal(t, spec.Env["EXECD_ACCESS_TOKEN"], fixture.create.Env["EXECD_ACCESS_TOKEN"])
		after, found, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, sandboxID, after.SandboxID)
		require.True(t, after.Submitted)
		require.NoError(t, reconcile(steps, before.ID))
		require.Equal(t, 1, fixture.creates)
		require.ErrorIs(t, steps.executions.Save(ctx, stale), domain.ErrConcurrentModification, "a stale execution cannot overwrite the binding")
		input.Capabilities.Prompt = "changed capabilities"
		require.ErrorIs(t, beginAgain(input), domain.ErrExecutionAdmission)
	})

	t.Run("lost response is recovered by lookup with no second POST", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.createPending = true
		fixture.loseCreateResponse = true
		provisioning := newProvisioning(provider, time.Now)
		input := request()
		input.WorkID = "ambiguous"
		begun, err := register(ctx, provisioning, input, testMounts)
		require.NoError(t, err)
		require.Error(t, reconcilerOf(t, db, provisioning).once(ctx, begun.ID), "the create's answer is lost")
		record, found, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, record.Submitted)
		require.Empty(t, record.SandboxID)
		require.NotNil(t, fixture.info, "external allocation survived failed response")
		provisioning = newProvisioning(provider, time.Now)
		require.NoError(t, reconcile(reconcilerOf(t, db, provisioning), begun.ID))
		record, _, err = latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.Equal(t, sandboxID, record.SandboxID)
		require.Equal(t, 1, fixture.creates)
	})

	t.Run("refused create rejects the start and retires without deletion", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.createPending = true
		fixture.refuseStatus = http.StatusBadRequest
		provisioning := newProvisioning(provider, time.Now)
		steps := reconcilerOf(t, db, provisioning)
		input := request()
		input.WorkID = "refused"
		begun, err := register(ctx, provisioning, input, testMounts)
		require.NoError(t, err)
		require.NoError(t, reconcile(steps, begun.ID))
		record, found, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, record.Submitted)
		require.True(t, record.StartRejected, "a refused create proves no compute exists")
		rejected := current(t, steps, begun.ID)
		require.True(t, reported(rejected, domain.ExecutionStartRejected{WorkID: input.WorkID, StartAdmittedAt: rejected.StartAdmittedAt}))
		require.NoError(t, reconcile(steps, begun.ID), "a rejected start is settled, never submitted again")
		_, err = rejected.RequestExit(domain.WorkStopRequested{WorkID: input.WorkID, StartAdmittedAt: rejected.StartAdmittedAt, StopRequestID: "stop-refused"})
		require.NoError(t, err)
		require.NoError(t, steps.executions.Save(ctx, rejected))
		require.NoError(t, reconcile(steps, begun.ID))
		require.Empty(t, steps.harness.Stops, "a rejected start has no process to stop and needs no Dispatcher")
		require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByRejection, time.Now()))
		require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByRejection, time.Now()), "retirement is idempotent")
		require.NoError(t, reconcile(steps, begun.ID))
		require.Equal(t, domain.ExecutionReleased, current(t, steps, begun.ID).State, "the Work may start again")
		require.Equal(t, 1, fixture.creates)
		require.Zero(t, fixture.deletes)
	})

	t.Run("refused create keeps a sandbox the provider created anyway", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.createPending = true
		fixture.refuseStatus, fixture.createOnRefusal = http.StatusBadRequest, true
		provisioning := newProvisioning(provider, time.Now)
		input := request()
		input.WorkID = "refused-but-created"
		begun, err := register(ctx, provisioning, input, testMounts)
		require.NoError(t, err)
		require.NoError(t, reconcile(reconcilerOf(t, db, provisioning), begun.ID))
		record, found, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, record.StartRejected, "existing compute is never retired as a rejected start")
		require.Equal(t, sandboxID, record.SandboxID)
		require.Equal(t, 1, fixture.creates)
	})

	t.Run("concurrent workers cannot double submit", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.createPending = true
		provisioning := newProvisioning(provider, time.Now)
		input := request()
		input.WorkID = "concurrent"
		begun, err := register(ctx, provisioning, input, testMounts)
		require.NoError(t, err)
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			// Separate workers retain the cross-process CAS race.
			worker := reconcilerOf(t, db, newProvisioning(provider, time.Now))
			workers.Go(func() { <-start; results <- reconcile(worker, begun.ID) })
		}
		close(start)
		workers.Wait()
		close(results)
		for err := range results {
			require.True(t, err == nil || errors.Is(err, domain.ErrConcurrentModification), "unexpected worker error: %v", err)
		}
		require.NoError(t, reconcile(reconcilerOf(t, db, provisioning), begun.ID))
		require.Equal(t, 1, fixture.creates)
		record, _, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.Equal(t, sandboxID, record.SandboxID)
	})

	t.Run("empty lookup cannot authorize blind resubmission", func(t *testing.T) {
		provider, _, fixture := sandboxClient(t)
		fixture.createPending = true
		provisioning := newProvisioning(provider, time.Now)
		steps := reconcilerOf(t, db, provisioning)
		input := request()
		input.WorkID = "uncertain-no-observation"
		begun, err := register(ctx, provisioning, input, testMounts)
		require.NoError(t, err)
		// Crash after the durable submission but before POST is
		// indistinguishable from an external request still in flight; absence
		// is not definitive failure.
		run := current(t, steps, begun.ID)
		require.True(t, run.RecordSubmission())
		require.NoError(t, steps.executions.Save(ctx, run))
		claimed, _, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.NoError(t, reconcile(steps, begun.ID))
		require.NoError(t, reconcile(steps, begun.ID))
		require.Zero(t, fixture.creates)
		record, _, err := latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.Empty(t, record.SandboxID)
		require.True(t, record.Submitted)
		require.Equal(t, claimed.UpdatedAt, record.UpdatedAt, "empty lookup must not rewrite the execution's record")
		require.Equal(t, claimed.Version, record.Version)
		// A late external observation still discharges the same create.
		var spec sdk.CreateSandboxRequest
		require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
		fixture.mu.Lock()
		fixture.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: spec.Metadata, Status: sdk.SandboxStatus{State: "Pending"}}
		fixture.mu.Unlock()
		require.NoError(t, reconcile(steps, begun.ID))
		record, _, err = latestAllocation(ctx, db, input.WorkID)
		require.NoError(t, err)
		require.Equal(t, sandboxID, record.SandboxID)
		require.Zero(t, fixture.creates)
	})
}
