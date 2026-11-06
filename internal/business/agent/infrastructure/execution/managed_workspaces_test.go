package execution

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	pathpkg "path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// machineFiles is a machine's Work root as the relay serves it: listings,
// file reads and the inputs manifest of the Work "session".
type machineFiles struct {
	mu          sync.Mutex
	unreachable bool
	unknown     bool
	dirs        map[string][]machineFileEntry
	files       map[string]string
	manifest    []byte
	reads       []string
	// listed are the directories listed, in order.
	listed []string
}

func (m *machineFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unreachable {
		http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		return
	}
	base := "/relay/" + managedComputerID + "/works/session"
	if r.Method == http.MethodPut && r.URL.Path == base+"/inputs" {
		if r.Header.Get("X-Talgent-Control-Token") != "control" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		m.manifest, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if r.Header.Get("X-EXECD-ACCESS-TOKEN") != "execd-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if m.unknown {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	path := r.URL.Query().Get("path")
	switch r.URL.Path {
	case base + "/files":
		m.listed = append(m.listed, path)
		entries, ok := m.dirs[path]
		if !ok {
			if _, file := m.files[path]; file {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
	case base + "/files/stat":
		dir, name := pathpkg.Split(path)
		for _, entry := range m.dirs[strings.TrimSuffix(dir, "/")] {
			if entry.Name == name {
				_ = json.NewEncoder(w).Encode(entry)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case base + "/files/content":
		content, ok := m.files[path]
		if !ok {
			if _, dir := m.dirs[path]; dir {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		m.reads = append(m.reads, path)
		_, _ = io.WriteString(w, content)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// judgedFile is what Agent handed Storage's preview judgement.
type judgedFile struct {
	Name string
	Size int64
	Head int
}

// storageJudge stands for Storage's Preview Policy: a .log is truncated to
// shownLog bytes, a .bin is refused, anything else is shown whole.
type storageJudge struct{ judged []judgedFile }

const shownLog, wholeLimit = 100, 1000

func (j *storageJudge) JudgeFilePreview(_ context.Context, name string, size int64, head []byte) (application.WorkspaceFilePreview, int64, error) {
	j.judged = append(j.judged, judgedFile{Name: name, Size: size, Head: len(head)})
	switch {
	case strings.HasSuffix(name, ".log"):
		return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewTruncated, Display: application.WorkspacePreviewText, LimitBytes: shownLog}, shownLog, nil
	case strings.HasSuffix(name, ".bin"):
		return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewRefused, Refusal: application.WorkspacePreviewUnsupported}, 0, nil
	}
	return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewDirect, Display: application.WorkspacePreviewText}, wholeLimit, nil
}

func newMachineFiles(t *testing.T) (*machineFiles, *ManagedComputers, *ManagedWorkspaces, managedSpecification) {
	t.Helper()
	modified := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	big := strings.Repeat("a", previewHeadBytes+10)
	machine := &machineFiles{
		dirs: map[string][]machineFileEntry{
			"":       {{Name: "inputs", Kind: "directory", ModifiedAt: modified}, {Name: "notes.md", Kind: "file", Size: 5, ModifiedAt: modified}, {Name: "out", Kind: "directory", ModifiedAt: modified}},
			"inputs": {{Name: "brief.pdf", Kind: "file", Size: 9, ModifiedAt: modified}},
			"out":    {{Name: "big.log", Kind: "file", Size: int64(len(big)), ModifiedAt: modified}, {Name: "data.bin", Kind: "file", Size: 3, ModifiedAt: modified}},
		},
		files: map[string]string{"notes.md": "# hi\n", "inputs/brief.pdf": "%PDF-1.7\n", "out/big.log": big, "out/data.bin": "\x00\x01\x02"},
	}
	server := httptest.NewServer(machine)
	t.Cleanup(server.Close)
	machines, err := NewManagedComputers(server.Client(), server.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
	require.NoError(t, err)
	links, err := NewWorkspaceFileLinks("/api/v1/agent/workspace-files", "control", time.Now)
	require.NoError(t, err)
	workspaces := &ManagedWorkspaces{machines: machines, links: links, previews: &storageJudge{}}
	spec := managedSpecification{ComputerID: managedComputerID, AccessToken: "execd-token"}
	return machine, machines, workspaces, spec
}

// The machine downloads each Attachment from the Gateway's content address
// on its own origin, with the grant Storage signed; nothing else of the
// Access Link travels.
func TestManagedInputsHandTheMachineTheWholeListOfAttachments(t *testing.T) {
	machine, machines, _, spec := newMachineFiles(t)
	updated := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	require.NoError(t, machines.deliverInputs(context.Background(), spec, "session", []InputAttachment{
		{Name: "brief.pdf", SizeBytes: 1024, UpdatedAt: updated, AccessPath: "/v1/storage/content?grant=signed.grant"},
	}))
	require.JSONEq(t, `{"files":[{"name":"brief.pdf","size":1024,"updated_at":"2026-11-02T10:00:00Z","url":"`+managedContentAddress+`?grant=signed.grant"}]}`, string(machine.manifest))

	require.NoError(t, machines.deliverInputs(context.Background(), spec, "session", nil))
	require.JSONEq(t, `{"files":[]}`, string(machine.manifest), "an emptied input source empties inputs/")
	require.Error(t, machines.deliverInputs(context.Background(), spec, "session", []InputAttachment{{Name: "x", AccessPath: "/v1/storage/content"}}), "a link without a grant is never sent")

	machine.unreachable = true
	require.ErrorIs(t, machines.deliverInputs(context.Background(), spec, "session", nil), errMachineUnreachable)
}

// Talgent Computer downloads inputs only from the origin it sends the Work's
// identity to, so the content address must be there.
func TestManagedComputersNeedTheContentAddressOnTheGatewaysOrigin(t *testing.T) {
	for _, address := range []string{"", "https://elsewhere.example/computer/v1/storage/content", "http://workbench.example/computer/v1/storage/content", "https://workbench.example", "https://workbench.example/c?x=1"} {
		_, err := NewManagedComputers(http.DefaultClient, "http://relay.invalid", ManagedAddresses{Gateway: managedGatewayAddress, Content: address, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
		require.Error(t, err, address)
	}
}

// A Managed Work's directory is browsed on its machine: Attachments under
// inputs/ name themselves, a missing path is not found, a machine the relay
// does not reach is a Disconnection, and a machine that does not know the
// Work never opened its directory.
func TestManagedWorkspaceIsBrowsedOnTheMachine(t *testing.T) {
	ctx := context.Background()
	machine, _, workspaces, spec := newMachineFiles(t)
	listing, err := workspaces.view(ctx, spec, "session", "")
	require.NoError(t, err)
	require.Equal(t, 3, listing.Total)
	require.Equal(t, application.WorkspaceDirectory, listing.Entries[0].Kind)
	require.Equal(t, application.WorkspaceFile, listing.Entries[1].Kind)
	require.Empty(t, listing.Entries[1].AttachmentName)
	inputs, err := workspaces.view(ctx, spec, "session", "inputs")
	require.NoError(t, err)
	require.Equal(t, "brief.pdf", inputs.Entries[0].AttachmentName, "an input is read as its Attachment")

	_, err = workspaces.view(ctx, spec, "session", "missing")
	require.ErrorIs(t, err, application.ErrWorkspacePathNotFound)
	_, err = workspaces.view(ctx, spec, "session", "../etc")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath)
	_, err = workspaces.view(ctx, spec, "session", "notes.md")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath)

	machine.unknown = true
	_, err = workspaces.view(ctx, spec, "session", "")
	require.ErrorIs(t, err, application.ErrWorkspaceNotCreated)
	machine.unknown, machine.unreachable = false, true
	_, err = workspaces.view(ctx, spec, "session", "")
	require.ErrorIs(t, err, application.ErrWorkspaceUnavailable)
	_, err = workspaces.preview(ctx, spec, "session", "notes.md")
	require.ErrorIs(t, err, application.ErrWorkspaceUnavailable)
	_, err = workspaces.downloadLink(ctx, spec, "session", "notes.md")
	require.ErrorIs(t, err, application.ErrWorkspaceUnavailable)
}

// Storage judges each preview from the file's name, size and at most its
// text probe of leading bytes; a readable file is linked for at most the
// bytes Storage allows.
func TestManagedWorkspacePreviewsAreJudgedByStorage(t *testing.T) {
	ctx := context.Background()
	machine, _, workspaces, spec := newMachineFiles(t)
	preview, err := workspaces.preview(ctx, spec, "session", "notes.md")
	require.NoError(t, err)
	require.Equal(t, application.WorkspacePreviewDirect, preview.Mode)
	require.Equal(t, application.WorkspacePreviewText, preview.Display)
	require.NotNil(t, preview.Link)
	grant := linkGrant(t, workspaces.links, preview.Link.URL)
	require.Equal(t, workspaceFileGrant{WorkID: "session", Path: "notes.md", Limit: wholeLimit, ExpiresAt: grant.ExpiresAt}, grant)

	preview, err = workspaces.preview(ctx, spec, "session", "out/big.log")
	require.NoError(t, err)
	require.Equal(t, application.WorkspacePreviewTruncated, preview.Mode)
	require.Equal(t, int64(shownLog), preview.LimitBytes)
	require.Equal(t, int64(shownLog), linkGrant(t, workspaces.links, preview.Link.URL).Limit)

	preview, err = workspaces.preview(ctx, spec, "session", "out/data.bin")
	require.NoError(t, err)
	require.Equal(t, application.WorkspaceFilePreview{Mode: application.WorkspacePreviewRefused, Refusal: application.WorkspacePreviewUnsupported}, preview)
	require.Equal(t, []judgedFile{
		{Name: "notes.md", Size: 5, Head: 5}, {Name: "big.log", Size: previewHeadBytes + 10, Head: previewHeadBytes}, {Name: "data.bin", Size: 3, Head: 3},
	}, workspaces.previews.(*storageJudge).judged)

	_, err = workspaces.preview(ctx, spec, "session", "inputs/brief.pdf")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath, "an Attachment is previewed as itself")
	_, err = workspaces.preview(ctx, spec, "session", "out")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath)
	_, err = workspaces.preview(ctx, spec, "session", "gone.txt")
	require.ErrorIs(t, err, application.ErrWorkspacePathNotFound)
	require.Empty(t, machine.listed, "a file is described on its own, never by listing its directory")

	link, err := workspaces.downloadLink(ctx, spec, "session", "out/data.bin")
	require.NoError(t, err)
	download := linkGrant(t, workspaces.links, link.URL)
	require.True(t, download.Download)
	require.Zero(t, download.Limit, "a download is the whole file")
	require.WithinDuration(t, time.Now().Add(workspaceFileLinkTTL), link.ExpiresAt, 2*time.Second)
}

func linkGrant(t *testing.T, links *WorkspaceFileLinks, link string) workspaceFileGrant {
	t.Helper()
	parsed, err := url.Parse(link)
	require.NoError(t, err)
	require.Equal(t, "/api/v1/agent/workspace-files", parsed.Path)
	grant, err := links.verify(parsed.Query().Get("grant"))
	require.NoError(t, err)
	return grant
}

// A link reads only what it was signed for, until it expires.
func TestWorkspaceFileLinksRefuseAlteredAndExpiredGrants(t *testing.T) {
	now := time.Now()
	links, err := NewWorkspaceFileLinks("/api/v1/agent/workspace-files", "secret", func() time.Time { return now })
	require.NoError(t, err)
	link, _ := links.issue(workspaceFileGrant{WorkID: "w", Path: "a.txt"})
	parsed, _ := url.Parse(link)
	token := parsed.Query().Get("grant")
	_, err = links.verify(token)
	require.NoError(t, err)
	encoded, signature, _ := strings.Cut(token, ".")
	other, _ := NewWorkspaceFileLinks("/api/v1/agent/workspace-files", "another", func() time.Time { return now })
	for _, forged := range []string{"", encoded, encoded + ".x", strings.ToUpper(encoded) + "." + signature} {
		_, err = links.verify(forged)
		require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink, forged)
	}
	_, err = other.verify(token)
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink, "another key verifies nothing")
	now = now.Add(workspaceFileLinkTTL + time.Second)
	_, err = links.verify(token)
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink, "an expired link reads nothing")
	for _, address := range []string{"", "/", "/x?y=1"} {
		_, err = NewWorkspaceFileLinks(address, "secret", time.Now)
		require.Error(t, err, address)
	}
	_, err = NewWorkspaceFileLinks("/x", " ", time.Now)
	require.Error(t, err)
}

// A file read the relay stops answering fails once it waits past its idle
// bound, for the response or for its next bytes, instead of holding the
// reader's response open.
func TestAStalledWorkFileReadFails(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") == "silent.txt" {
			<-release
			return
		}
		_, _ = io.WriteString(w, "part")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer server.Close()
	defer close(release)
	machines, err := NewManagedComputers(server.Client(), server.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
	require.NoError(t, err)
	machines.fileIdle = 200 * time.Millisecond
	spec := managedSpecification{ComputerID: managedComputerID, AccessToken: "execd-token"}

	started := time.Now()
	content, err := machines.openFile(context.Background(), spec, "session", "stalled.txt")
	require.NoError(t, err)
	read, err := io.ReadAll(content)
	require.Error(t, err)
	require.Equal(t, "part", string(read))
	require.NoError(t, content.Close())
	_, err = machines.openFile(context.Background(), spec, "session", "silent.txt")
	require.Error(t, err, "no response within the bound")
	require.Less(t, time.Since(started), 5*time.Second)
}
