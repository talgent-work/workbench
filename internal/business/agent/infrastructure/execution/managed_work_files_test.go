package execution

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	pathpkg "path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
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

// openedToken is the execd token the machine keeps for the Work "session".
type openedToken string

func (o openedToken) accessToken(context.Context, string) (string, error) { return string(o), nil }

// sessionDirectory is where the Work "session" has its files.
var sessionDirectory = domain.WorkspaceLocation{WorkID: "session", ComputerID: managedComputerID}

func newMachineFiles(t *testing.T) (*machineFiles, *ManagedComputers, *ManagedWorkFiles, managedSpecification) {
	t.Helper()
	modified := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	machine := &machineFiles{
		dirs: map[string][]machineFileEntry{
			"":       {{Name: "inputs", Kind: "directory", ModifiedAt: modified}, {Name: "notes.md", Kind: "file", Size: 5, ModifiedAt: modified}},
			"inputs": {{Name: "brief.pdf", Kind: "file", Size: 9, ModifiedAt: modified}},
		},
		files: map[string]string{"notes.md": "# hi\n", "inputs/brief.pdf": "%PDF-1.7\n"},
	}
	server := httptest.NewServer(machine)
	t.Cleanup(server.Close)
	machines, err := NewManagedComputers(server.Client(), server.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
	require.NoError(t, err)
	files := &ManagedWorkFiles{tokens: openedToken("execd-token"), machines: machines}
	spec := managedSpecification{ComputerID: managedComputerID, AccessToken: "execd-token"}
	return machine, machines, files, spec
}

// The machine downloads each Attachment from the Gateway's content address
// on its own origin, with the grant Storage signed; nothing else of the
// Access Link travels.
func TestManagedInputsHandTheMachineTheWholeListOfAttachments(t *testing.T) {
	machine, machines, _, spec := newMachineFiles(t)
	updated := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	require.NoError(t, machines.deliverInputs(context.Background(), spec, "session", []command.InputFile{
		{Name: "brief.pdf", SizeBytes: 1024, UpdatedAt: updated, AccessPath: "/v1/storage/content?grant=signed.grant"},
	}))
	require.JSONEq(t, `{"files":[{"name":"brief.pdf","size":1024,"updated_at":"2026-11-02T10:00:00Z","url":"`+managedContentAddress+`?grant=signed.grant"}]}`, string(machine.manifest))

	require.NoError(t, machines.deliverInputs(context.Background(), spec, "session", nil))
	require.JSONEq(t, `{"files":[]}`, string(machine.manifest), "an emptied inputs Directory empties inputs/")
	require.Error(t, machines.deliverInputs(context.Background(), spec, "session", []command.InputFile{{Name: "x", AccessPath: "/v1/storage/content"}}), "a link without a grant is never sent")

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

// A Managed Work's files are read on its machine with the execd token it
// keeps: a missing path is not found, a directory read as a file is not of
// the kind read, and a machine the relay does not reach is a Disconnection.
func TestManagedWorkFilesAreReadThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	machine, _, files, _ := newMachineFiles(t)
	listed, err := files.List(ctx, sessionDirectory, "")
	require.NoError(t, err)
	require.Equal(t, []WorkFile{{Name: "inputs", Directory: true, ModifiedAt: machine.dirs[""][0].ModifiedAt}, {Name: "notes.md", Size: 5, ModifiedAt: machine.dirs[""][1].ModifiedAt}}, listed)
	described, err := files.Stat(ctx, sessionDirectory, "inputs/brief.pdf")
	require.NoError(t, err)
	require.Equal(t, int64(9), described.Size)
	content, err := files.Open(ctx, sessionDirectory, "notes.md")
	require.NoError(t, err)
	read, err := io.ReadAll(content)
	require.NoError(t, content.Close())
	require.NoError(t, err)
	require.Equal(t, "# hi\n", string(read))

	_, err = files.List(ctx, sessionDirectory, "missing")
	require.ErrorIs(t, err, ErrWorkFileNotFound)
	_, err = files.List(ctx, sessionDirectory, "notes.md")
	require.ErrorIs(t, err, ErrWorkFileNotRegular)
	_, err = files.Open(ctx, sessionDirectory, "inputs")
	require.ErrorIs(t, err, ErrWorkFileNotRegular)

	machine.unreachable = true
	_, err = files.List(ctx, sessionDirectory, "")
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
	_, err = files.Stat(ctx, sessionDirectory, "notes.md")
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
	_, err = files.Open(ctx, sessionDirectory, "notes.md")
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
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
