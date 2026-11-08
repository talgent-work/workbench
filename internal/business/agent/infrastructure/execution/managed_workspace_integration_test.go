//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
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

// A Managed Work with an input source has its Attachments listed to its
// machine once its credentials are delivered, recording the source's owner;
// Storage's report of a change in that File Space marks its inputs due on
// their own, never its credentials, and their own step syncs them; a change
// in any other space leaves it alone. A failing sync never fails the start:
// the inputs stay due for the next reconciliation.
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
	owner := "case-" + uuid.NewString()
	f.inputs.owners = map[string]string{"file-space/case-1": owner}
	f.inputs.attachments = map[string][]InputAttachment{owner: {{Name: "brief.pdf", SizeBytes: 1024, UpdatedAt: updated, AccessPath: "/v1/storage/content?grant=first"}}}

	input := withInputs(managedRequest())
	record := f.launch(t, f.begin(t, input).ID)
	require.Equal(t, []manifestFile{{Name: "brief.pdf", Size: 1024, UpdatedAt: "2026-11-02T10:00:00Z", URL: managedContentAddress + "?grant=first"}}, lastManifest(t, f.relay))
	require.True(t, f.relay.afterDelivery[0], "the machine downloads with the Work credential delivered first")
	require.Positive(t, f.inputs.bound)
	require.LessOrEqual(t, f.inputs.bound, reconciliationAttemptTimeout, "a sync never holds up the start for long")
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, owner, record.InputOwner)
	require.Zero(t, record.InputsDue)
	require.Positive(t, record.InputsSyncedAt)

	require.Empty(t, f.markInputsChanged(t, "case-other"))

	f.inputs.attachments[owner] = append(f.inputs.attachments[owner], InputAttachment{Name: "data.csv", SizeBytes: 3, UpdatedAt: updated, AccessPath: "/v1/storage/content?grant=second"})
	require.Equal(t, []string{record.ID}, f.markInputsChanged(t, owner))
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
	owner := "case-" + uuid.NewString()
	f.inputs.owners = map[string]string{"file-space/case-1": owner}
	f.inputs.attachments = map[string][]InputAttachment{owner: {{Name: "brief.pdf", SizeBytes: 1, UpdatedAt: time.Now(), AccessPath: "/v1/storage/content?grant=first"}}}
	input := withInputs(managedRequest())
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
	f.inputs.attachments[owner][0].AccessPath = "/v1/storage/content?grant=second"
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
	f.inputs.err = errors.New("storage unavailable")
	input := withInputs(managedRequest())
	record := f.launch(t, f.begin(t, input).ID)
	require.True(t, record.CredentialsDelivered, "the inputs are not a Required Startup Step")
	require.Zero(t, record.InputsSyncedAt, "the inputs stay due")
	require.Empty(t, f.relay.manifests)

	f.inputs.err = nil
	owner := "case-" + uuid.NewString()
	f.inputs.owners = map[string]string{"file-space/case-1": owner}
	require.NoError(t, f.steps.shortSteps(ctx, record.ID))
	require.Empty(t, lastManifest(t, f.relay))
	synced, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, owner, synced.InputOwner)
	require.Zero(t, synced.InputsDue)
	require.Positive(t, synced.InputsSyncedAt)
}

// cloudViews stands for Storage's Workspace View of a Cloud Work.
type cloudViews struct {
	StorageWorkspaces
	reads []string
}

func (c *cloudViews) WorkspaceView(_ context.Context, location domain.WorkspaceLocation, _ string) (application.WorkspaceListing, error) {
	c.reads = append(c.reads, location.WorkID)
	return application.WorkspaceListing{}, nil
}

// A Managed Work's workspace is read on its machine through the relay with
// the execd token its machine keeps, and its links stream from there; while
// the machine is away it is unavailable. A Cloud location is Storage's to
// read, and a link never opens a Cloud Work's file.
func TestManagedWorkspaceIsReadThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	f.relay.set(func(m *machineRelay) { m.files = map[string]string{"notes.md": "# notes\n"} })
	input := managedRequest()
	f.launch(t, f.begin(t, input).ID)

	links, err := application.NewWorkspaceFileSigner("/api/v1/agent/workspace-files", "computer-control", time.Now)
	require.NoError(t, err)
	managed, err := NewManagedWorkspaces(f.db, f.provisioning.managed.machines, links, &storageJudge{})
	require.NoError(t, err)
	cloud := &cloudViews{}
	views, err := NewWorkspaces(cloud, managed)
	require.NoError(t, err)
	directory := domain.WorkspaceLocation{WorkID: input.WorkID, ComputerID: managedComputerID}

	listing, err := views.WorkspaceView(ctx, directory, "")
	require.NoError(t, err)
	require.Equal(t, 1, listing.Total)
	require.Equal(t, "notes.md", listing.Entries[0].Name)
	preview, err := views.PreviewWorkspaceFile(ctx, directory, "notes.md")
	require.NoError(t, err)
	require.Equal(t, application.WorkspacePreviewDirect, preview.Mode)
	link, err := views.WorkspaceFileLink(ctx, directory, "notes.md")
	require.NoError(t, err)
	parsed, err := url.Parse(link.URL)
	require.NoError(t, err)
	grant, err := links.Verify(parsed.Query().Get("grant"))
	require.NoError(t, err)
	opened, err := views.OpenWorkspaceFile(ctx, directory, grant)
	require.NoError(t, err)
	content, err := io.ReadAll(opened.Content)
	opened.Content.Close()
	require.NoError(t, err)
	require.Equal(t, "# notes\n", string(content))
	require.True(t, opened.Download)
	require.Equal(t, "notes.md", opened.Name)

	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	_, err = views.WorkspaceView(ctx, directory, "")
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
	_, err = views.OpenWorkspaceFile(ctx, directory, grant)
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
	require.Empty(t, cloud.reads)

	cloudWork := domain.WorkspaceLocation{WorkID: uuid.NewString(), SessionWorkspace: "workspace"}
	_, err = views.WorkspaceView(ctx, cloudWork, "")
	require.NoError(t, err)
	require.Equal(t, []string{cloudWork.WorkID}, cloud.reads)
	_, err = views.OpenWorkspaceFile(ctx, cloudWork, grant)
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink)
}
