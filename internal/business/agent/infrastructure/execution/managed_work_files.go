package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ManagedWorkFiles reads a Managed Work's directory on its machine through
// Computer's relay. Paths are relative to the Work root, which is laid out
// as a Cloud Work's /workspace. It judges nothing about the files: the
// workspace reader decides how they are shown.
type ManagedWorkFiles struct {
	tokens   workAccessTokens
	machines *ManagedComputers
}

func NewManagedWorkFiles(db *xorm.Engine, machines *ManagedComputers) (*ManagedWorkFiles, error) {
	if db == nil || machines == nil {
		return nil, errors.New("agent: executions and Managed Computers are required for Managed Works' files")
	}
	return &ManagedWorkFiles{tokens: recordedAccessTokens{db: db}, machines: machines}, nil
}

// WorkFile is one entry of a Managed Work's directory.
type WorkFile struct {
	Name       string
	Directory  bool
	Size       int64
	ModifiedAt time.Time
}

// List lists one directory of the Work root, "" for the root.
// ErrWorkFileNotFound at the root means the machine does not know the Work:
// it never opened its directory.
func (f *ManagedWorkFiles) List(ctx context.Context, location domain.WorkspaceLocation, dir string) ([]WorkFile, error) {
	spec, err := f.reach(ctx, location)
	if err != nil {
		return nil, err
	}
	entries, err := f.machines.listFiles(ctx, spec, location.WorkID, dir)
	if err != nil {
		return nil, readError(err)
	}
	files := make([]WorkFile, 0, len(entries))
	for _, entry := range entries {
		files = append(files, entry.file())
	}
	return files, nil
}

// Stat describes one entry of the Work root, a file or a directory.
func (f *ManagedWorkFiles) Stat(ctx context.Context, location domain.WorkspaceLocation, path string) (WorkFile, error) {
	spec, err := f.reach(ctx, location)
	if err != nil {
		return WorkFile{}, err
	}
	entry, err := f.machines.statFile(ctx, spec, location.WorkID, path)
	if err != nil {
		return WorkFile{}, readError(err)
	}
	return entry.file(), nil
}

// Open streams one file of the Work root; the caller closes it.
func (f *ManagedWorkFiles) Open(ctx context.Context, location domain.WorkspaceLocation, path string) (io.ReadCloser, error) {
	spec, err := f.reach(ctx, location)
	if err != nil {
		return nil, err
	}
	content, err := f.machines.openFile(ctx, spec, location.WorkID, path)
	if err != nil {
		return nil, readError(err)
	}
	return content, nil
}

func (e machineFileEntry) file() WorkFile {
	return WorkFile{Name: e.Name, Directory: e.Kind == machineDirectory, Size: e.Size, ModifiedAt: e.ModifiedAt}
}

// readError names a machine the relay does not reach as a Disconnection of
// the workspace.
func readError(err error) error {
	if errors.Is(err, errMachineUnreachable) {
		return domain.ErrWorkspaceUnavailable
	}
	return err
}

// workAccessTokens finds the execd token of the environment a machine last
// opened for a Work, which it keeps for the Work's life; empty for a Work
// whose environment was never opened.
type workAccessTokens interface {
	accessToken(ctx context.Context, workID string) (string, error)
}

// recordedAccessTokens reads the token from the Work's latest execution that
// opened an environment.
type recordedAccessTokens struct{ db *xorm.Engine }

func (r recordedAccessTokens) accessToken(ctx context.Context, workID string) (string, error) {
	var record executionRecord
	found, err := r.db.Context(ctx).Where("session_id = ? AND sandbox_id <> ''", workID).
		Desc("created_at").Desc("id").Cols("specification_json").Get(&record)
	if err != nil {
		return "", oops.With("operation", "find_work_access_token").Wrap(err)
	}
	if !found {
		return "", nil
	}
	var opened struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(record.SpecificationJSON, &opened); err != nil {
		return "", oops.With("operation", "find_work_access_token").Wrap(err)
	}
	return opened.AccessToken, nil
}

// reach is how the Work's directory is reached: through the Computer its
// location names, with its execd token. A Work without one is asked for all
// the same, and its machine answers that it does not know the Work.
func (f *ManagedWorkFiles) reach(ctx context.Context, location domain.WorkspaceLocation) (managedSpecification, error) {
	token, err := f.tokens.accessToken(ctx, location.WorkID)
	if err != nil {
		return managedSpecification{}, err
	}
	return managedSpecification{ComputerID: location.ComputerID, AccessToken: token}, nil
}
