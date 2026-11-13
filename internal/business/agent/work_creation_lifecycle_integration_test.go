//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// An AgentDefinition is never one of its own Owners, so it creates no Work.
func TestCreationRefusesTheDefinitionAsOwner(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	m.start()
	request := subjectCreation(definition.ID, "work-self")
	request.SubjectId = definition.ID
	_, err := m.client.CreateWork(m.control(), request)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "the AgentDefinition identity is rejected as owner")
	require.Empty(t, launches(t, m.db))
}

// A creation the Cloud has no capacity for leaves no Work to resume later,
// and restoring the capacity revives nothing.
func TestCapacityRejectedCreationLeavesNoWork(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	m.start()
	full := availableQuota()
	full.Status.Used = full.Status.Hard.DeepCopy()
	_, err := m.capacity.CoreV1().ResourceQuotas("sandboxes").UpdateStatus(m.ctx, full, metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = m.client.CreateWork(m.control(), subjectCreation(definition.ID, "work-a"))
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	_, err = m.capacity.CoreV1().ResourceQuotas("sandboxes").UpdateStatus(m.ctx, availableQuota(), metav1.UpdateOptions{})
	require.NoError(t, err)
	absent := m.locate(definition.ID, "work-a")
	require.False(t, absent.Found)
	require.Empty(t, absent.WorkId)
	require.Empty(t, launches(t, m.db), "restoring quota cannot revive a rejected creation")
}

// Concurrent private creations for one Work Key resolve one Work, which takes
// the requested capability differences and begins one execution.
func TestConcurrentCreationResolvesOneWork(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	m.start()
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 8)
	barrier := make(chan struct{})
	for range 8 {
		go func() {
			<-barrier
			response, err := m.client.CreateWork(m.control(), subjectCreation(definition.ID, "work-a"))
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{id: response.WorkId}
		}()
	}
	close(barrier)
	var workID string
	for range 8 {
		created := <-results
		require.NoError(t, created.err)
		if workID == "" {
			workID = created.id
		}
		require.Equal(t, workID, created.id, "concurrent private creation must resolve one Work for the Work Key")
	}
	requests := awaitLaunches(t, m.db, 1, "one Work Key begins one execution")
	work := m.get(workID)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STARTING, work.State)
	require.Equal(t, workPrompt, work.Capabilities.Prompt)
	require.Equal(t, workStartupAgent, work.Capabilities.StartupAgent)
	require.Equal(t, workID, requests[0].WorkID)
	require.Equal(t, workOwner, requests[0].RepresentedPerson)
	require.Equal(t, domain.ContextFresh, requests[0].ContextMode)
	require.Equal(t, workPrompt, requests[0].Capabilities.Prompt)
	require.Equal(t, workStartupAgent, requests[0].Capabilities.StartupAgent)
}

// A definition's capability change never reaches its existing Works: the
// Work Key still locates its Work. A new Work of another Work Key takes the
// change, whether the existing Work runs or was closed, with its own HOME and
// Harness Session in the definition's shared Harness state.
func TestDefinitionChangeAppliesOnlyToNewWorks(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	existing := map[domain.WorkState]seededWork{}
	for _, state := range []domain.WorkState{domain.WorkWorking, domain.WorkClosed} {
		existing[state] = seedWork(t, m.db, definition, workSeed{subject: "existing-" + string(state), state: state, closeRequest: closeOf(state)})
	}
	m.start()
	_, err := m.client.ChangeAgentDefinitionCapabilities(m.control(), &agentprivatev1.ChangeAgentDefinitionCapabilitiesRequest{AgentId: definition.ID, Capabilities: &agentprivatev1.Capabilities{Prompt: "changed base"}})
	require.NoError(t, err)
	for state, work := range existing {
		t.Run(string(state), func(t *testing.T) {
			located, err := m.client.CreateWork(m.control(), subjectCreation(definition.ID, work.WorkKey.ID))
			require.NoError(t, err)
			require.Equal(t, work.ID, located.WorkId, "the Work Key locates its Work whatever the definition's changes")
			require.Equal(t, workPrompt, m.get(work.ID).Capabilities.Prompt)
			subject := "new-" + string(state)
			absent := m.locate(definition.ID, subject)
			require.False(t, absent.Found, "a Work Key without a Work locates none")
			require.Empty(t, absent.WorkId)
			request := subjectCreation(definition.ID, subject)
			request.CapabilityDifferences = nil
			created, err := m.client.CreateWork(m.control(), request)
			require.NoError(t, err, "no definition limits how many of its Works run")
			fresh := m.get(created.WorkId)
			require.Equal(t, "changed base", fresh.Capabilities.Prompt)
			require.NotEqual(t, work.HarnessSessionReference, fresh.ContextReference)
			launch := m.runs.await(t, m.ctx, created.WorkId, func(*domain.AgentExecution) bool { return true }, "the new Work begins its execution").Launch
			require.NotEqual(t, work.LocatedLayout.Mounts[0], launch.LocatedLayout.Mounts[0], "each Work has its own HOME")
			require.NotZero(t, harnessState(launch))
			require.Equal(t, harnessState(work.LaunchSpecification()), harnessState(launch), "the definition's Works share its Harness state")
			require.NotEqual(t, work.HarnessSessionReference, launch.HarnessSessionReference)
		})
	}
	require.Len(t, launches(t, m.db), 4, "only the new Works begin executions")
}

// harnessState is the Directory a start mounts as the Harness's state.
func harnessState(launch domain.LaunchSpecification) domain.LocatedDirectory {
	for _, mount := range launch.LocatedLayout.Mounts {
		if mount.Path == "$HOME/.claude" {
			return mount.Directory
		}
	}
	return domain.LocatedDirectory{}
}

// closeOf is the close a Closed Work applied; other states keep none.
func closeOf(state domain.WorkState) string {
	if state == domain.WorkClosed {
		return "close"
	}
	return ""
}
