//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

// Sandboxes of a later allocation and of an allocation Agent has no record of.
const (
	surplusSandboxID = "5f0e3c1a-7b2d-4e8f-9a6b-0c1d2e3f4a5b"
	foreignSandboxID = "9c8b7a6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

type releaseFixture struct {
	db           *xorm.Engine
	provisioning *Provisioning
	steps        *executionSteps
	lookups      *infrastructure.ExecutionQueryRepository
	lifecycle    *lifecycleFixture
	record       allocationRecord
	// metadata is what the execution's sandboxes carry.
	metadata map[string]string
}

// newRelease begins one Work's Cloud execution whose creation was submitted
// and whose Startup Deadline, with its abandonment margin, has passed.
func newRelease(t *testing.T) *releaseFixture {
	t.Helper()
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	credentials := fixtureSandboxCredentials(db, provider)
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	record, err := register(ctx, p, request(), testMounts)
	require.NoError(t, err)
	connections, err := NewNativeConnections(p, credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	steps := newExecutionSteps(t, p, connections, unread{}, nil, time.Now)
	run, err := steps.executions.Get(ctx, record.ID)
	require.NoError(t, err)
	require.True(t, run.RecordSubmission())
	require.NoError(t, steps.executions.Save(ctx, run))
	seated := time.Now().Add(-domain.StartupDeadline - 2*time.Minute).UnixMilli()
	_, err = db.Exec("UPDATE execution_allocation SET start_admitted_at = ?, seated_at = ?, created_at = ? WHERE id = ?", seated, seated, seated, record.ID)
	require.NoError(t, err)
	var spec sdk.CreateSandboxRequest
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	return &releaseFixture{db: db, provisioning: p, steps: steps, lookups: infrastructure.NewExecutionQueryRepository(db), lifecycle: lifecycle, record: record, metadata: spec.Metadata}
}

// abandon reconciles the start, which no environment answered, past its
// Startup Deadline: it is abandoned and its Work retires it.
func (f *releaseFixture) abandon(t *testing.T) {
	t.Helper()
	f.reconcile(t)
	run := f.execution(t)
	require.True(t, run.Outcome.Abandoned)
	require.Empty(t, run.Environment, "no environment carrying its ownership was observed")
	f.retire(t, domain.RetiredByAbandonment)
}

// retire retires the execution as its Work asks.
func (f *releaseFixture) retire(t *testing.T, cause domain.RetirementCause) {
	t.Helper()
	require.NoError(t, f.steps.retire(context.Background(), f.record.SessionID, cause, time.Now()))
}

func (f *releaseFixture) execution(t *testing.T) *domain.AgentExecution {
	t.Helper()
	run, err := f.steps.executions.Get(context.Background(), f.record.ID)
	require.NoError(t, err)
	return run
}

// sandbox places a sandbox carrying metadata on the environment authority.
func (f *releaseFixture) sandbox(id string, metadata map[string]string) {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	info := sdk.SandboxInfo{ID: id, Metadata: metadata, Status: sdk.SandboxStatus{State: sdk.StateRunning}}
	if id == sandboxID {
		f.lifecycle.info = &info
		return
	}
	if f.lifecycle.others == nil {
		f.lifecycle.others = map[string]sdk.SandboxInfo{}
	}
	f.lifecycle.others[id] = info
}

// present lists the sandboxes the environment authority still holds.
func (f *releaseFixture) present() []string {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	ids := []string{}
	for _, item := range f.lifecycle.listed("") {
		ids = append(ids, item.ID)
	}
	return ids
}

// reconcile runs the execution's recovery as its task would.
func (f *releaseFixture) reconcile(t *testing.T) {
	t.Helper()
	require.NoError(t, f.steps.reconcile(context.Background(), f.record.ID))
}

func (f *releaseFixture) deletes() int {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	return f.lifecycle.deletes
}

func (f *releaseFixture) outstanding(t *testing.T) bool {
	t.Helper()
	ids, err := f.lookups.ListOutstandingExecutions(context.Background(), "", 100)
	require.NoError(t, err)
	return slices.Contains(ids, f.record.ID)
}

// released reports the execution's release confirmed and reported to its
// Work.
func (f *releaseFixture) released(t *testing.T) bool {
	t.Helper()
	run := f.execution(t)
	if run.State != domain.ExecutionReleased {
		return false
	}
	require.True(t, reported(run, domain.ExecutionReleasedEvent{WorkID: run.WorkID, StartAdmittedAt: run.StartAdmittedAt}))
	require.False(t, run.Desk.LeftAt.IsZero(), "its Desk left before the release was confirmed")
	return true
}

// A submitted start whose sandbox identity was never recorded is retired
// without an environment conflict, and its release deletes every sandbox
// carrying its ownership, never one whose execution Agent cannot name. The
// release is confirmed to the Work only once the authority, observed again
// after the deletions, shows none of them, and only then its Desk leaves.
func TestARetiredAllocationWithoutASandboxIdentityReleasesEverySandboxItOwns(t *testing.T) {
	f := newRelease(t)
	f.abandon(t)
	f.sandbox(sandboxID, f.metadata)
	f.sandbox(surplusSandboxID, f.metadata)
	stranger := correlation(Allocation{WorkID: f.record.SessionID, ID: "01900000-0000-7000-8000-0000000000ff"})
	stranger[sandboxSpecLabel] = f.metadata[sandboxSpecLabel]
	f.sandbox(foreignSandboxID, stranger)

	f.retire(t, domain.RetiredByAbandonment)
	retired := f.execution(t)
	require.Equal(t, domain.ExecutionRetired, retired.State, "retirement is idempotent")
	require.True(t, retired.Desk.LeftAt.IsZero(), "its Desk is held while it owns environments")
	require.True(t, f.outstanding(t), "a retired execution stays outstanding until released")
	require.False(t, f.released(t))

	f.reconcile(t)
	require.Equal(t, []string{foreignSandboxID}, f.present(), "every owned sandbox is deleted; one Agent cannot prove it owns stays")
	require.Equal(t, 2, f.deletes())
	require.True(t, f.released(t), "release is confirmed once the authority shows no owned sandbox")
	require.Equal(t, []string{retired.Desk.ID}, f.steps.desks.departures(), "the Desk leaves after the deletions were observed")
	require.False(t, f.outstanding(t))

	released := f.execution(t)
	f.reconcile(t)
	require.Equal(t, released.Version, f.execution(t).Version, "a released execution is not released again")
	require.Equal(t, 2, f.deletes())
	require.Equal(t, []string{foreignSandboxID}, f.present())
}

// A retired execution that owns no sandbox confirms its release at once. The
// release is recorded on the execution, which reports it to its Work, so a
// Work that missed the report still applies it from the execution.
func TestARetiredAllocationWithoutSandboxesConfirmsItsReleaseAtOnce(t *testing.T) {
	f := newRelease(t)
	f.abandon(t)
	f.reconcile(t)
	require.True(t, f.released(t))
	require.False(t, f.outstanding(t), "the recorded release is not released again")
	released := f.execution(t)
	f.reconcile(t)
	require.Equal(t, released.Version, f.execution(t).Version, "a released execution is not released again")
	require.Len(t, f.steps.desks.departures(), 1)
	require.Zero(t, f.deletes())
}

// A previously bound sandbox the list no longer shows is confirmed absent by
// its own identity before the release is confirmed.
func TestABoundSandboxIsConfirmedAbsentByItsIdentityBeforeRelease(t *testing.T) {
	f := newRelease(t)
	ctx := context.Background()
	run := f.execution(t)
	_, err := run.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: sandboxID})
	require.NoError(t, err)
	run.Environment = sandboxID
	_, err = run.RecordLaunch("process")
	require.NoError(t, err)
	run.RecordExit("process", domain.HarnessExit{})
	require.NoError(t, f.steps.executions.Save(ctx, run))
	f.sandbox(sandboxID, f.metadata)
	f.lifecycle.pages = [][]sdk.SandboxInfo{{}} // A list that has not caught up yet.

	f.retire(t, domain.RetiredByExit)
	f.reconcile(t)
	require.Equal(t, 1, f.deletes(), "the bound sandbox is deleted though the list missed it")
	require.Empty(t, f.present())
	require.True(t, f.released(t), "release follows once its identity shows it absent")
}

// A sandbox the list returns without the execution's exact ownership is left
// in place and holds back neither the deletion of the owned ones nor the
// release once they are gone.
func TestAListedStrangerHoldsBackNoRelease(t *testing.T) {
	f := newRelease(t)
	f.abandon(t)
	stranger := correlation(Allocation{WorkID: f.record.SessionID, ID: "01900000-0000-7000-8000-0000000000ff"})
	f.sandbox(foreignSandboxID, stranger)
	f.sandbox(sandboxID, f.metadata)
	f.lifecycle.pages = [][]sdk.SandboxInfo{{f.lifecycle.others[foreignSandboxID], *f.lifecycle.info}} // A list that disregards its filter.

	f.retire(t, domain.RetiredByAbandonment)
	f.reconcile(t)
	require.Equal(t, []string{foreignSandboxID}, f.present(), "only the owned sandbox is deleted")
	f.lifecycle.pages = [][]sdk.SandboxInfo{{f.lifecycle.others[foreignSandboxID]}}
	f.reconcile(t)
	require.True(t, f.released(t), "the stranger does not hold back the release")
	require.Equal(t, []string{foreignSandboxID}, f.present())
}
