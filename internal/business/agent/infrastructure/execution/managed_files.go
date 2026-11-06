package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/samber/oops"
)

// The Work's files on its machine, read through the relay with the Work's
// execd token. Paths are
// relative to the Work root, which is laid out as a Cloud Work's /workspace.

var (
	// errWorkFileNotFound means the machine has no such directory or file
	// under the Work root, or the path leaves it.
	errWorkFileNotFound = errors.New("agent: no such file in the Work's directory")
	// errWorkFileNotRegular means a directory was read as a file, or a file
	// listed as a directory.
	errWorkFileNotRegular = errors.New("agent: the path is not of the kind read")
)

// machineFileKind is what an entry of the Work root is, as the machine names
// it.
type machineFileKind string

const (
	machineDirectory machineFileKind = "directory"
	machineFile      machineFileKind = "file"
)

// machineFileEntry is one entry of a directory of the Work root.
type machineFileEntry struct {
	Name       string          `json:"name"`
	Kind       machineFileKind `json:"kind"`
	Size       int64           `json:"size"`
	ModifiedAt time.Time       `json:"modified_at"`
}

// maxListingBytes bounds the body of one directory listing.
const maxListingBytes = 16 << 20

// listFiles lists one directory of the Work root.
func (m *ManagedComputers) listFiles(ctx context.Context, spec managedSpecification, workID, path string) ([]machineFileEntry, error) {
	response, err := m.readFiles(ctx, spec, workID, "/files", path, "list_work_files")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var listing struct {
		Entries []machineFileEntry `json:"entries"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxListingBytes)).Decode(&listing); err != nil {
		return nil, oops.With("operation", "list_work_files").Wrap(err)
	}
	return listing.Entries, nil
}

// maxStatBytes bounds the body of one file's description.
const maxStatBytes = 64 << 10

// statFile describes one entry of the Work root, a file or a directory.
func (m *ManagedComputers) statFile(ctx context.Context, spec managedSpecification, workID, path string) (machineFileEntry, error) {
	response, err := m.readFiles(ctx, spec, workID, "/files/stat", path, "stat_work_file")
	if err != nil {
		return machineFileEntry{}, err
	}
	defer response.Body.Close()
	var entry machineFileEntry
	if err := json.NewDecoder(io.LimitReader(response.Body, maxStatBytes)).Decode(&entry); err != nil {
		return machineFileEntry{}, oops.With("operation", "stat_work_file").Wrap(err)
	}
	return entry, nil
}

// workFileIdle bounds how long a Work file read waits for its next bytes, as
// the relay bounds the machine's (computergateway.FileIdle), so a stalled
// relay never holds a reader's response open.
const workFileIdle = 30 * time.Second

// openFile streams one file of the Work root as the machine reads it; the
// caller closes it. A read that waits past fileIdle for its response or its
// next bytes fails.
func (m *ManagedComputers) openFile(ctx context.Context, spec managedSpecification, workID, path string) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	idle := time.AfterFunc(m.fileIdle, cancel)
	response, err := m.readFiles(ctx, spec, workID, "/files/content", path, "read_work_file")
	if err != nil {
		idle.Stop()
		cancel()
		return nil, err
	}
	idle.Reset(m.fileIdle)
	return &idleBody{body: response.Body, idle: idle, after: m.fileIdle, cancel: cancel}, nil
}

// idleBody is a response body whose request is canceled once a read waits
// longer than after for its next bytes.
type idleBody struct {
	body   io.ReadCloser
	idle   *time.Timer
	after  time.Duration
	cancel context.CancelFunc
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.idle.Reset(b.after)
	return n, err
}

func (b *idleBody) Close() error {
	b.idle.Stop()
	b.cancel()
	return b.body.Close()
}

// readFiles makes one Work files call and returns its successful response.
func (m *ManagedComputers) readFiles(ctx context.Context, spec managedSpecification, workID, call, path, operation string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint(spec, workID)+call+"?"+url.Values{"path": {path}}.Encode(), nil)
	if err != nil {
		return nil, oops.Wrap(err)
	}
	request.Header.Set("X-EXECD-ACCESS-TOKEN", spec.AccessToken)
	response, err := m.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, oops.With("operation", operation).Wrap(err)
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	switch response.StatusCode {
	case http.StatusNotFound:
		return nil, errWorkFileNotFound
	case http.StatusBadRequest:
		return nil, errWorkFileNotRegular
	case http.StatusServiceUnavailable:
		return nil, errMachineUnreachable
	}
	return nil, oops.With("operation", operation, "status", response.StatusCode).Errorf("agent: relay returned HTTP %d", response.StatusCode)
}
