//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
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
	environments *Environments
	recovery     *EnvironmentRecovery
	observations *ProcessObservations
	lifecycle    *lifecycleFixture
	feedback     *reclamationFeedback
	record       allocationRecord
	// metadata is what the allocation's sandboxes carry.
	metadata map[string]string
}

// newRelease registers one Work's Cloud allocation whose create was submitted
// and whose Startup Deadline, with its abandonment margin, has passed.
func newRelease(t *testing.T) *releaseFixture {
	t.Helper()
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	input := request()
	require.NoError(t, p.Register(ctx, input, testMounts))
	started := time.Now().Add(-domain.StartupDeadline - abandonmentMargin - time.Second).UnixMilli()
	_, err = db.Exec("UPDATE execution_allocation SET submitted = 1, created_at = ? WHERE session_id = ?", started, input.WorkID)
	require.NoError(t, err)
	record, _, err := latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	var spec sdk.CreateSandboxRequest
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	observations := allocationObserverFixture(t, p, dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING)
	environments, err := NewEnvironments(p, observations)
	require.NoError(t, err)
	feedback := &reclamationFeedback{}
	connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", feedback, command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	reclaimer, err := NewReclaimer(feedback)
	require.NoError(t, err)
	recovery, err := NewEnvironmentRecovery(p, connections, reclaimer, unread{})
	require.NoError(t, err)
	return &releaseFixture{db: db, provisioning: p, environments: environments, recovery: recovery, observations: observations, lifecycle: lifecycle, feedback: feedback, record: record, metadata: spec.Metadata}
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

// reconcile runs the allocation's recovery as its task would.
func (f *releaseFixture) reconcile(t *testing.T) {
	t.Helper()
	_, err := f.recovery.ReconcileAllocation(context.Background(), f.record.ID)
	require.NoError(t, err)
}

func (f *releaseFixture) confirmations() int {
	confirmed := 0
	for _, request := range f.feedback.requests {
		if request.WorkID == f.record.SessionID && request.Kind == command.ComputeReclaimed {
			confirmed++
		}
	}
	return confirmed
}

func (f *releaseFixture) outstanding(t *testing.T) bool {
	t.Helper()
	statuses, err := f.recovery.ListOutstandingAllocations(context.Background(), "", 100)
	require.NoError(t, err)
	for _, status := range statuses {
		if status.AllocationID == f.record.ID {
			return true
		}
	}
	return false
}

func (f *releaseFixture) reclaimed(t *testing.T) bool {
	t.Helper()
	observation, err := f.observations.ObserveProcess(context.Background(), f.record.SessionID)
	require.NoError(t, err)
	return observation.ComputeReclaimed
}

// A submitted start whose sandbox identity was never recorded is retired
// without an environment conflict, and its release deletes every sandbox
// carrying its ownership, never one whose allocation Agent cannot name, before
// confirming the release to the Work.
func TestARetiredAllocationWithoutASandboxIdentityReleasesEverySandboxItOwns(t *testing.T) {
	f := newRelease(t)
	f.sandbox(sandboxID, f.metadata)
	f.sandbox(surplusSandboxID, f.metadata)
	stranger := correlation(Allocation{WorkID: f.record.SessionID, ID: "01900000-0000-7000-8000-0000000000ff"})
	stranger[sandboxSpecLabel] = f.metadata[sandboxSpecLabel]
	f.sandbox(foreignSandboxID, stranger)

	require.NoError(t, f.environments.Delete(context.Background(), f.record.SessionID), "submitted with no sandbox identity is no conflict")
	require.NoError(t, f.environments.Delete(context.Background(), f.record.SessionID), "retirement is idempotent")
	_, live, err := f.provisioning.live(context.Background(), f.record.SessionID)
	require.NoError(t, err)
	require.False(t, live)
	require.True(t, f.outstanding(t), "a retired allocation stays outstanding until released")
	require.False(t, f.reclaimed(t))

	f.reconcile(t)
	require.Equal(t, []string{foreignSandboxID}, f.present(), "every owned sandbox is deleted; one Agent cannot prove it owns stays")
	require.Zero(t, f.confirmations(), "an accepted deletion is not yet absence")

	f.reconcile(t)
	require.Equal(t, 1, f.confirmations(), "release is confirmed once no owned sandbox remains")
	require.False(t, f.outstanding(t))
	require.True(t, f.reclaimed(t), "the Work observes the confirmed release")
	require.Equal(t, []string{foreignSandboxID}, f.present())
}

// A retired allocation that owns no sandbox confirms its release at once. The
// release is recorded before Agent learns of it, so an Agent that cannot take
// the confirmation still observes the release in its own recovery.
func TestARetiredAllocationWithoutSandboxesConfirmsItsReleaseAtOnce(t *testing.T) {
	f := newRelease(t)
	require.NoError(t, f.environments.Delete(context.Background(), f.record.SessionID))
	f.feedback.err = errors.New("agent unavailable")
	_, err := f.recovery.ReconcileAllocation(context.Background(), f.record.ID)
	require.Error(t, err, "a lost confirmation is reported")
	require.Equal(t, 1, f.confirmations(), "no hidden confirmation retry")
	require.False(t, f.outstanding(t), "the recorded release is not released again")
	require.True(t, f.reclaimed(t), "the Work's observation finds the recorded release")
	f.feedback.err = nil
	f.reconcile(t)
	require.Equal(t, 1, f.confirmations(), "a released allocation is not released again")
	require.Zero(t, f.lifecycle.deletes)
}

// A previously bound sandbox the list no longer shows is confirmed absent by
// its own identity before the release is confirmed.
func TestABoundSandboxIsConfirmedAbsentByItsIdentityBeforeRelease(t *testing.T) {
	f := newRelease(t)
	_, err := f.db.Exec("UPDATE execution_allocation SET sandbox_id = ?, process_id = 'process', process_exited = 1 WHERE id = ?", sandboxID, f.record.ID)
	require.NoError(t, err)
	f.sandbox(sandboxID, f.metadata)
	f.lifecycle.pages = [][]sdk.SandboxInfo{{}} // A list that has not caught up yet.

	require.NoError(t, f.environments.Delete(context.Background(), f.record.SessionID))
	f.reconcile(t)
	require.Zero(t, f.confirmations(), "the bound sandbox still exists")
	require.Equal(t, 1, f.lifecycle.deletes, "the bound sandbox is deleted though the list missed it")

	f.reconcile(t)
	require.Equal(t, 1, f.confirmations())
	require.True(t, f.reclaimed(t))
}

// A sandbox the list returns without the allocation's exact ownership is left
// in place and holds back neither the deletion of the owned ones nor the
// release once they are gone.
func TestAListedStrangerHoldsBackNoRelease(t *testing.T) {
	f := newRelease(t)
	stranger := correlation(Allocation{WorkID: f.record.SessionID, ID: "01900000-0000-7000-8000-0000000000ff"})
	f.sandbox(foreignSandboxID, stranger)
	f.sandbox(sandboxID, f.metadata)
	f.lifecycle.pages = [][]sdk.SandboxInfo{{f.lifecycle.others[foreignSandboxID], *f.lifecycle.info}} // A list that disregards its filter.

	require.NoError(t, f.environments.Delete(context.Background(), f.record.SessionID))
	f.reconcile(t)
	require.Equal(t, []string{foreignSandboxID}, f.present(), "only the owned sandbox is deleted")
	f.lifecycle.pages = [][]sdk.SandboxInfo{{f.lifecycle.others[foreignSandboxID]}}
	f.reconcile(t)
	require.Equal(t, 1, f.confirmations(), "the stranger does not hold back the release")
	require.Equal(t, []string{foreignSandboxID}, f.present())
}
