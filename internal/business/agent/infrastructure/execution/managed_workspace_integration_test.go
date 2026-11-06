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
// the inputs stay due for the recovery sweep.
func TestManagedWorkReceivesItsCurrentInputs(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	updated := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	owner := "case-" + uuid.NewString()
	f.inputs.owners = map[string]string{"file-space/case-1": owner}
	f.inputs.attachments = map[string][]InputAttachment{owner: {{Name: "brief.pdf", SizeBytes: 1024, UpdatedAt: updated, AccessPath: "/v1/storage/content?grant=first"}}}

	input := withInputs(managedRequest())
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.NoError(t, submitPending(ctx, f.provisioning))
	require.NoError(t, connectLive(ctx, f.connections))
	require.Equal(t, []manifestFile{{Name: "brief.pdf", Size: 1024, UpdatedAt: "2026-11-02T10:00:00Z", URL: managedContentAddress + "?grant=first"}}, lastManifest(t, f.relay))
	require.True(t, f.relay.afterDelivery[0], "the machine downloads with the Work credential delivered first")
	require.Positive(t, f.inputs.bound)
	require.LessOrEqual(t, f.inputs.bound, firstInputsSyncBound, "the first sync never holds up the start for long")
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, owner, record.InputOwner)
	require.Zero(t, record.InputsDue)

	unrelated, err := f.recovery.MarkInputsChanged(ctx, "case-other")
	require.NoError(t, err)
	require.Empty(t, unrelated)

	f.inputs.attachments[owner] = append(f.inputs.attachments[owner], InputAttachment{Name: "data.csv", SizeBytes: 3, UpdatedAt: updated, AccessPath: "/v1/storage/content?grant=second"})
	due, err := f.recovery.MarkInputsChanged(ctx, owner)
	require.NoError(t, err)
	require.Equal(t, []string{record.ID}, due)
	marked, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.EqualValues(t, 1, marked.InputsDue, "the change stays due until it is synced")
	require.Equal(t, record.CredentialsExpireAt, marked.CredentialsExpireAt, "the credentials are not due")
	require.Equal(t, record.Version, marked.Version, "a mark never conflicts with the allocation's other steps")
	// Dispatcher reads the Work's process, so nothing else is missing.
	f.recovery.owners = &readingProcess{reading: StreamReading{ProcessID: marked.ProcessID}}
	step, err := f.recovery.missingStep(ctx, marked)
	require.NoError(t, err)
	require.Equal(t, application.AllocationSyncInputs, step, "the sweep finds the due inputs")

	// The machine is away: the inputs stay due for the sweep.
	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	require.Error(t, f.recovery.SyncInputs(ctx, record.ID))
	f.relay.set(func(m *machineRelay) { m.unreachable = false })
	require.NoError(t, f.recovery.SyncInputs(ctx, record.ID))
	require.Len(t, lastManifest(t, f.relay), 2)
	synced, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Zero(t, synced.InputsDue)
	deliveries := f.relay.deliveries
	require.NoError(t, f.recovery.SyncInputs(ctx, record.ID), "nothing is due")
	require.Equal(t, deliveries, f.relay.deliveries, "a sync delivers no credentials")
}

// Synced inputs are synced again once they are older than the refresh
// interval, with fresh Access Links, though nothing changed: the sweep marks
// them due and syncs them. Fresh ones are left alone.
func TestManagedInputsAreRefreshedBeforeTheirLinksExpire(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	owner := "case-" + uuid.NewString()
	f.inputs.owners = map[string]string{"file-space/case-1": owner}
	f.inputs.attachments = map[string][]InputAttachment{owner: {{Name: "brief.pdf", SizeBytes: 1, UpdatedAt: time.Now(), AccessPath: "/v1/storage/content?grant=first"}}}
	input := withInputs(managedRequest())
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.NoError(t, submitPending(ctx, f.provisioning))
	require.NoError(t, connectLive(ctx, f.connections))
	synced, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Positive(t, synced.InputsSyncedAt, "a sync records when it happened")
	require.Zero(t, synced.InputsDue)
	f.recovery.owners = &readingProcess{reading: StreamReading{ProcessID: synced.ProcessID}}
	step := func() application.AllocationStep {
		statuses, err := f.recovery.ListOutstandingAllocations(ctx, "", 100)
		require.NoError(t, err)
		for _, status := range statuses {
			if status.AllocationID == synced.ID {
				return status.Step
			}
		}
		t.Fatalf("allocation %s not listed", synced.ID)
		return 0
	}
	require.Equal(t, application.AllocationSettled, step(), "fresh inputs need nothing")

	stale := time.Now().Add(-managedInputsRefresh - time.Minute).UnixMilli()
	_, err = f.db.Exec(`UPDATE execution_allocation SET inputs_synced_at = ? WHERE id = ?`, stale, synced.ID)
	require.NoError(t, err)
	f.inputs.attachments[owner][0].AccessPath = "/v1/storage/content?grant=second"
	require.Equal(t, application.AllocationSyncInputs, step(), "the sweep syncs inputs older than the refresh interval")
	require.NoError(t, f.recovery.SyncInputs(ctx, synced.ID))
	require.Equal(t, managedContentAddress+"?grant=second", lastManifest(t, f.relay)[0].URL)
	refreshed, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Zero(t, refreshed.InputsDue)
	require.Greater(t, refreshed.InputsSyncedAt, stale)
	require.Equal(t, application.AllocationSettled, step())
}

// A start whose inputs cannot be listed still becomes ready: its inputs stay
// due until their own step syncs them.
func TestAFailingInputsSyncLeavesTheStartReady(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	f.inputs.err = errors.New("storage unavailable")
	input := withInputs(managedRequest())
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.NoError(t, submitPending(ctx, f.provisioning))
	require.NoError(t, connectLive(ctx, f.connections))
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.True(t, record.SandboxReady, "the inputs are not a Required Startup Step")
	require.EqualValues(t, 1, record.InputsDue)
	require.Empty(t, f.relay.manifests)

	f.inputs.err = nil
	owner := "case-" + uuid.NewString()
	f.inputs.owners = map[string]string{"file-space/case-1": owner}
	require.NoError(t, f.recovery.SyncInputs(ctx, record.ID))
	require.Empty(t, lastManifest(t, f.relay))
	synced, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, owner, synced.InputOwner)
	require.Zero(t, synced.InputsDue)
}

// cloudViews stands for Storage's Workspace View of a Cloud Work.
type cloudViews struct{ reads []string }

func (c *cloudViews) WorkspaceView(_ context.Context, workspace application.ViewedWorkspace, _ string) (application.WorkspaceListing, error) {
	c.reads = append(c.reads, workspace.WorkID)
	return application.WorkspaceListing{}, nil
}

func (c *cloudViews) PreviewWorkspaceFile(context.Context, application.ViewedWorkspace, string) (application.WorkspaceFilePreview, error) {
	return application.WorkspaceFilePreview{}, nil
}

func (c *cloudViews) WorkspaceFileLink(context.Context, application.ViewedWorkspace, string) (application.WorkspaceFileLink, error) {
	return application.WorkspaceFileLink{}, nil
}

// A Managed Work's workspace is read on its machine through the relay, and
// its links stream from there; while the machine is away it is unavailable.
// A Work the Managed realization never opened is Storage's to read from its
// Session Workspace; one without either has nothing to read yet.
func TestManagedWorkspaceIsReadThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	f.relay.set(func(m *machineRelay) { m.files = map[string]string{"notes.md": "# notes\n"} })
	input := managedRequest()
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.NoError(t, submitPending(ctx, f.provisioning))
	require.NoError(t, connectLive(ctx, f.connections))

	links, err := NewWorkspaceFileLinks("/api/v1/agent/workspace-files", "computer-control", time.Now)
	require.NoError(t, err)
	managed, err := NewManagedWorkspaces(f.db, f.provisioning.managed.machines, links, &storageJudge{})
	require.NoError(t, err)
	cloud := &cloudViews{}
	views, err := NewWorkspaceViews(cloud, managed)
	require.NoError(t, err)
	// A Managed Work has no Session Workspace.
	workspace := application.ViewedWorkspace{WorkID: input.WorkID}

	listing, err := views.WorkspaceView(ctx, workspace, "")
	require.NoError(t, err)
	require.Equal(t, 1, listing.Total)
	require.Equal(t, "notes.md", listing.Entries[0].Name)
	preview, err := views.PreviewWorkspaceFile(ctx, workspace, "notes.md")
	require.NoError(t, err)
	require.Equal(t, application.WorkspacePreviewDirect, preview.Mode)
	link, err := views.WorkspaceFileLink(ctx, workspace, "notes.md")
	require.NoError(t, err)
	parsed, err := url.Parse(link.URL)
	require.NoError(t, err)
	opened, err := managed.OpenLink(ctx, parsed.Query().Get("grant"))
	require.NoError(t, err)
	content, err := io.ReadAll(opened.Content)
	opened.Content.Close()
	require.NoError(t, err)
	require.Equal(t, "# notes\n", string(content))
	require.True(t, opened.Download)
	require.Equal(t, "notes.md", opened.Name)
	_, err = managed.OpenLink(ctx, parsed.Query().Get("grant")+"x")
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink)

	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	_, err = views.WorkspaceView(ctx, workspace, "")
	require.ErrorIs(t, err, application.ErrWorkspaceUnavailable)
	_, err = managed.OpenLink(ctx, parsed.Query().Get("grant"))
	require.ErrorIs(t, err, application.ErrWorkspaceUnavailable)
	require.Empty(t, cloud.reads)

	cloudWork := application.ViewedWorkspace{WorkID: uuid.NewString(), Reference: "workspace"}
	_, err = views.WorkspaceView(ctx, cloudWork, "")
	require.NoError(t, err)
	require.Equal(t, []string{cloudWork.WorkID}, cloud.reads)

	_, err = views.WorkspaceView(ctx, application.ViewedWorkspace{WorkID: uuid.NewString()}, "")
	require.ErrorIs(t, err, application.ErrWorkspaceNotCreated, "neither an opened directory nor a Session Workspace")
	require.Len(t, cloud.reads, 1)
}
