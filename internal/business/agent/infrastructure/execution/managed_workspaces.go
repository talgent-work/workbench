package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ManagedWorkspaces reads Managed Works' directories through the relay and
// serves their files behind Agent's own links.
type ManagedWorkspaces struct {
	tokens   workAccessTokens
	machines *ManagedComputers
	links    *application.WorkspaceFileSigner
	previews PreviewJudge
}

// PreviewJudge is Storage's Preview Policy for a workspace file it does not
// hold: how the file is shown, judged from its name, size and leading bytes,
// and the most bytes its reader is served.
type PreviewJudge interface {
	JudgeFilePreview(ctx context.Context, name string, size int64, head []byte) (application.WorkspaceFilePreview, int64, error)
}

// previewHeadBytes is how much of a file Storage judges, its text probe.
const previewHeadBytes = 8 << 10

func NewManagedWorkspaces(db *xorm.Engine, machines *ManagedComputers, links *application.WorkspaceFileSigner, previews PreviewJudge) (*ManagedWorkspaces, error) {
	if db == nil || machines == nil || links == nil || previews == nil {
		return nil, errors.New("agent: executions, Managed Computers, a link signer and Storage's preview judgement are required for Managed workspaces")
	}
	return &ManagedWorkspaces{tokens: recordedAccessTokens{db: db}, machines: machines, links: links, previews: previews}, nil
}

// maxWorkspaceListEntries bounds one directory's listing, as Storage's
// workspace-list-entries bounds a Cloud Work's.
const maxWorkspaceListEntries = 1000

// inputsDirectory is where a Work's Attachments are, in either realization.
const inputsDirectory = "inputs"

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
	var record allocationRecord
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
func (w *ManagedWorkspaces) reach(ctx context.Context, location domain.WorkspaceLocation) (managedSpecification, error) {
	token, err := w.tokens.accessToken(ctx, location.WorkID)
	if err != nil {
		return managedSpecification{}, err
	}
	return managedSpecification{ComputerID: location.ComputerID, AccessToken: token}, nil
}

// WorkspaceView lists one directory of the Work's directory. An entry under
// inputs/ is the input source's Attachment of its name, read as that
// Attachment.
func (w *ManagedWorkspaces) WorkspaceView(ctx context.Context, location domain.WorkspaceLocation, dir string) (application.WorkspaceListing, error) {
	clean, err := workspaceDirectory(dir)
	if err != nil {
		return application.WorkspaceListing{}, err
	}
	spec, err := w.reach(ctx, location)
	if err != nil {
		return application.WorkspaceListing{}, err
	}
	entries, err := w.machines.listFiles(ctx, spec, location.WorkID, clean)
	if clean == "" && errors.Is(err, errWorkFileNotFound) {
		// The machine does not know the Work: it never opened its directory.
		return application.WorkspaceListing{}, domain.ErrWorkspaceNotCreated
	}
	if err != nil {
		return application.WorkspaceListing{}, workspaceReadError(err)
	}
	listing := application.WorkspaceListing{Total: len(entries), Entries: make([]application.WorkspaceEntry, 0, min(len(entries), maxWorkspaceListEntries))}
	for _, entry := range entries[:min(len(entries), maxWorkspaceListEntries)] {
		kind := application.WorkspaceFile
		if entry.Kind == machineDirectory {
			kind = application.WorkspaceDirectory
		}
		mapped := application.WorkspaceEntry{Name: entry.Name, Kind: kind, SizeBytes: entry.Size, ModifiedAt: entry.ModifiedAt}
		if clean == inputsDirectory && kind == application.WorkspaceFile {
			mapped.AttachmentName = entry.Name
		}
		listing.Entries = append(listing.Entries, mapped)
	}
	return listing, nil
}

// PreviewWorkspaceFile has Storage judge one file as it judges a Cloud
// Work's, from its size and the start of its content, and links a readable
// one for inline reads of at most the bytes Storage allows.
func (w *ManagedWorkspaces) PreviewWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, file string) (application.WorkspaceFilePreview, error) {
	spec, clean, entry, err := w.file(ctx, location, file)
	if err != nil {
		return application.WorkspaceFilePreview{}, err
	}
	content, err := w.machines.openFile(ctx, spec, location.WorkID, clean)
	if err != nil {
		return application.WorkspaceFilePreview{}, workspaceReadError(err)
	}
	head, err := io.ReadAll(io.LimitReader(content, previewHeadBytes))
	content.Close()
	if err != nil {
		return application.WorkspaceFilePreview{}, workspaceReadError(oops.With("operation", "read_work_file").Wrap(err))
	}
	preview, served, err := w.previews.JudgeFilePreview(ctx, entry.Name, entry.Size, head)
	if err != nil {
		return application.WorkspaceFilePreview{}, err
	}
	if served > 0 {
		link := w.links.Issue(application.WorkspaceFileGrant{WorkID: location.WorkID, Path: clean, Limit: served})
		preview.Link = &link
	}
	return preview, nil
}

// WorkspaceFileLink links one existing file for download under its name.
func (w *ManagedWorkspaces) WorkspaceFileLink(ctx context.Context, location domain.WorkspaceLocation, file string) (application.WorkspaceFileLink, error) {
	_, clean, _, err := w.file(ctx, location, file)
	if err != nil {
		return application.WorkspaceFileLink{}, err
	}
	return w.links.Issue(application.WorkspaceFileGrant{WorkID: location.WorkID, Path: clean, Download: true}), nil
}

// file describes a file a reader opens outside inputs/, whose Attachments
// are read as themselves.
func (w *ManagedWorkspaces) file(ctx context.Context, location domain.WorkspaceLocation, file string) (managedSpecification, string, machineFileEntry, error) {
	clean, err := workspaceFile(file)
	if err != nil {
		return managedSpecification{}, "", machineFileEntry{}, err
	}
	spec, err := w.reach(ctx, location)
	if err != nil {
		return managedSpecification{}, "", machineFileEntry{}, err
	}
	entry, err := w.machines.statFile(ctx, spec, location.WorkID, clean)
	if err != nil {
		return managedSpecification{}, "", machineFileEntry{}, workspaceReadError(err)
	}
	if entry.Kind != machineFile {
		return managedSpecification{}, "", machineFileEntry{}, application.ErrInvalidWorkspacePath
	}
	return spec, clean, entry, nil
}

// OpenWorkspaceFile opens the file a verified link grants.
// domain.ErrWorkspaceUnavailable means the machine is not reachable now.
func (w *ManagedWorkspaces) OpenWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, grant application.WorkspaceFileGrant) (application.OpenedWorkspaceFile, error) {
	spec, err := w.reach(ctx, location)
	if err != nil {
		return application.OpenedWorkspaceFile{}, err
	}
	content, err := w.machines.openFile(ctx, spec, location.WorkID, grant.Path)
	if err != nil {
		return application.OpenedWorkspaceFile{}, workspaceReadError(err)
	}
	return application.OpenedWorkspaceFile{Name: path.Base(grant.Path), Download: grant.Download, Limit: grant.Limit, Content: content}, nil
}

// workspaceReadError names what reading the machine found as a workspace
// outcome; a machine the relay does not reach is a Disconnection.
func workspaceReadError(err error) error {
	switch {
	case errors.Is(err, errMachineUnreachable):
		return domain.ErrWorkspaceUnavailable
	case errors.Is(err, errWorkFileNotFound):
		return application.ErrWorkspacePathNotFound
	case errors.Is(err, errWorkFileNotRegular):
		return application.ErrInvalidWorkspacePath
	}
	return err
}

// workspaceDirectory is a directory relative to the Work root, "" for the
// root, as Storage accepts a Cloud Work's.
func workspaceDirectory(raw string) (string, error) {
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", nil
	}
	return relativeWorkspacePath(raw)
}

// workspaceFile is a file relative to the Work root outside inputs/.
func workspaceFile(raw string) (string, error) {
	clean, err := relativeWorkspacePath(strings.TrimPrefix(raw, "/"))
	if err != nil || clean == "." || clean == inputsDirectory || strings.HasPrefix(clean, inputsDirectory+"/") {
		return "", application.ErrInvalidWorkspacePath
	}
	return clean, nil
}

func relativeWorkspacePath(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\\\x00") {
		return "", application.ErrInvalidWorkspacePath
	}
	clean := path.Clean(raw)
	if clean != raw || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", application.ErrInvalidWorkspacePath
	}
	return clean, nil
}
