//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
)

// managedInputsRefresh is how long synced inputs stay current here.
const managedInputsRefresh = 10 * time.Minute

type manifestFile struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updated_at"`
	URL       string `json:"url"`
}

func lastManifest(t *testing.T, relay *machineRelay) []manifestFile {
	t.Helper()
	relay.mu.Lock()
	defer relay.mu.Unlock()
	require.NotEmpty(t, relay.manifests)
	var manifest struct {
		Files []manifestFile `json:"files"`
	}
	require.NoError(t, json.Unmarshal(relay.manifests[len(relay.manifests)-1], &manifest))
	return manifest.Files
}

// A Managed Work has the files of its located inputs Directory listed to its
// machine once its credentials are delivered, recording that Directory's
// key; Storage's report of a change in that Directory marks its inputs due
// on their own, never its credentials, and their own step syncs them; a
// change in any other Directory leaves it alone. A failing sync never fails
// the start: the inputs stay due for the next reconciliation.
// reading is Dispatcher reading the record's launched process at the relay
// endpoint it was handed.
func (f *managedWork) reading(t *testing.T, record executionRecord) StreamReading {
	t.Helper()
	spec, err := decodeManagedSpecification(record)
	require.NoError(t, err)
	return StreamReading{ProcessID: record.ProcessID, Endpoint: f.provisioning.managed.machines.endpoint(spec, record.SessionID)}
}

func TestManagedWorkReceivesItsCurrentInputs(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	updated := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	inputs := caseInputs()
	brief := managedFile{SpaceFile: command.SpaceFile{Name: "brief.pdf", SizeBytes: 1024, UpdatedAt: updated}, link: "/v1/storage/content?grant=first"}
	f.storage.place(inputs, brief)

	input := readingInputs(managedRequest(), inputs)
	record := f.launch(t, f.begin(t, input).ID)
	require.Equal(t, []manifestFile{{Name: "brief.pdf", Size: 1024, UpdatedAt: "2026-11-02T10:00:00Z", URL: managedContentAddress + "?grant=first"}}, lastManifest(t, f.relay))
	require.True(t, f.relay.afterDelivery[0], "the machine downloads with the Work credential delivered first")
	require.Positive(t, f.storage.bound)
	require.LessOrEqual(t, f.storage.bound, reconciliationAttemptTimeout, "a sync never holds up the start for long")
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, infrastructure.DirectoryKey(inputs), record.InputsDirectory)
	require.Zero(t, record.InputsDue)
	require.Positive(t, record.InputsSyncedAt)

	require.Empty(t, f.markInputsChanged(t, caseInputs()))

	f.storage.place(inputs, brief, managedFile{SpaceFile: command.SpaceFile{Name: "data.csv", SizeBytes: 3, UpdatedAt: updated}, link: "/v1/storage/content?grant=second"})
	require.Equal(t, []string{record.ID}, f.markInputsChanged(t, inputs))
	marked, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.EqualValues(t, 1, marked.InputsDue, "the change stays due until it is synced")
	require.Equal(t, record.CredentialsExpireAt, marked.CredentialsExpireAt, "the credentials are not due")
	// Dispatcher reads the Work's process, so nothing else is missing.
	f.owners.reading = f.reading(t, marked)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepSyncInputs}, f.decided(t, record.ID), "the due inputs are found")

	// The machine is away: its environment is observed not running, nothing
	// is synced and the inputs stay due.
	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	manifestsBefore := len(f.relay.manifests)
	require.NoError(t, f.steps.shortSteps(ctx, record.ID))
	require.Len(t, f.relay.manifests, manifestsBefore, "nothing is synced while the machine is away")
	away, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.EqualValues(t, 1, away.InputsDue, "the inputs stay due while the machine is away")
	f.relay.set(func(m *machineRelay) { m.unreachable = false })
	require.NoError(t, f.steps.shortSteps(ctx, record.ID))
	require.Len(t, lastManifest(t, f.relay), 2)
	synced, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Zero(t, synced.InputsDue)
	deliveries := f.relay.deliveries
	manifests := len(f.relay.manifests)
	require.NoError(t, f.steps.shortSteps(ctx, record.ID), "nothing is due")
	require.Equal(t, manifests, len(f.relay.manifests), "nothing is synced again")
	require.Equal(t, deliveries, f.relay.deliveries, "a sync delivers no credentials")
}

// Synced inputs are synced again once they are older than the refresh
// interval, with fresh Access Links, though nothing changed. Fresh ones are
// left alone.
func TestManagedInputsAreRefreshedBeforeTheirLinksExpire(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	inputs := caseInputs()
	brief := command.SpaceFile{Name: "brief.pdf", SizeBytes: 1, UpdatedAt: time.Now()}
	f.storage.place(inputs, managedFile{SpaceFile: brief, link: "/v1/storage/content?grant=first"})
	input := readingInputs(managedRequest(), inputs)
	synced := f.launch(t, f.begin(t, input).ID)
	synced, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Positive(t, synced.InputsSyncedAt, "a sync records when it happened")
	require.Zero(t, synced.InputsDue)
	f.owners.reading = f.reading(t, synced)
	require.Empty(t, f.decided(t, synced.ID), "fresh inputs need nothing")

	stale := time.Now().Add(-managedInputsRefresh - time.Minute).UnixMilli()
	_, err = f.db.Exec(`UPDATE agent_execution SET inputs_synced_at = ?, version = version + 1 WHERE id = ?`, stale, synced.ID)
	require.NoError(t, err)
	f.storage.place(inputs, managedFile{SpaceFile: brief, link: "/v1/storage/content?grant=second"})
	require.Equal(t, []domain.ExecutionStepKind{domain.StepSyncInputs}, f.decided(t, synced.ID), "inputs older than the refresh interval are synced")
	require.NoError(t, f.steps.shortSteps(ctx, synced.ID))
	require.Equal(t, managedContentAddress+"?grant=second", lastManifest(t, f.relay)[0].URL)
	refreshed, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Zero(t, refreshed.InputsDue)
	require.Greater(t, refreshed.InputsSyncedAt, stale)
	require.Empty(t, f.decided(t, synced.ID))
}

// A start whose inputs cannot be listed still becomes ready: its inputs stay
// due until their own step syncs them.
func TestAFailingInputsSyncLeavesTheStartReady(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	f.storage.err = errors.New("storage unavailable")
	inputs := caseInputs()
	input := readingInputs(managedRequest(), inputs)
	record := f.launch(t, f.begin(t, input).ID)
	require.True(t, record.CredentialsDelivered, "the inputs are not a Required Startup Step")
	require.Zero(t, record.InputsSyncedAt, "the inputs stay due")
	require.Empty(t, f.relay.manifests)

	f.storage.mu.Lock()
	f.storage.err = nil
	f.storage.mu.Unlock()
	require.NoError(t, f.steps.shortSteps(ctx, record.ID))
	require.Empty(t, lastManifest(t, f.relay))
	synced, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, infrastructure.DirectoryKey(inputs), synced.InputsDirectory)
	require.Zero(t, synced.InputsDue)
	require.Positive(t, synced.InputsSyncedAt)
}
