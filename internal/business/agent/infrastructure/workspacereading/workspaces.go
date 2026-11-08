// Package workspacereading reads a Work's workspace where its AgentWork
// locates it: Storage's Workspace View of a Cloud Work's Session Workspace,
// or a Managed Work's directory on its machine, reached through Computer.
package workspacereading

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// StorageViews is Storage's side of reading a workspace: the Workspace
// View of a Session Workspace, its previews and links, and the Preview
// Policy's judgement of a file Storage does not hold.
type StorageViews interface {
	WorkspaceView(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceListing, error)
	PreviewWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceFilePreview, error)
	WorkspaceFileLink(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceFileLink, error)
	// JudgeFilePreview tells how a file is shown, judged from its name, size
	// and leading bytes, and the most bytes its reader is served.
	JudgeFilePreview(ctx context.Context, name string, size int64, head []byte) (application.WorkspaceFilePreview, int64, error)
}

// Workspaces reads a Work's workspace at the location its AgentWork names: a
// directory on a Managed Computer through the relay, any other location from
// Storage's Session Workspace. This is the one place that tells the two
// apart. Only Managed Works' files are served behind Agent's own links.
type Workspaces struct {
	storage StorageViews
	managed *managedWorkspaces
}

var _ application.WorkspaceReadingPort = (*Workspaces)(nil)

// NewWorkspaces reads Cloud Works through Storage and, when the deployment
// reaches Managed Computers, Managed Works' files through files, linked by
// links; files and links are nil otherwise.
func NewWorkspaces(storage StorageViews, files managedFiles, links *application.WorkspaceFileSigner) (*Workspaces, error) {
	if storage == nil {
		return nil, errors.New("agent: Storage's workspaces are required")
	}
	workspaces := &Workspaces{storage: storage}
	if files != nil {
		if links == nil {
			return nil, errors.New("agent: a link signer is required for Managed workspaces")
		}
		workspaces.managed = &managedWorkspaces{files: files, links: links, previews: storage}
	}
	return workspaces, nil
}

func (v *Workspaces) WorkspaceView(ctx context.Context, location domain.WorkspaceLocation, dir string) (application.WorkspaceListing, error) {
	reader, err := v.reader(location)
	if err != nil {
		return application.WorkspaceListing{}, err
	}
	return reader.WorkspaceView(ctx, location, dir)
}

func (v *Workspaces) PreviewWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, file string) (application.WorkspaceFilePreview, error) {
	reader, err := v.reader(location)
	if err != nil {
		return application.WorkspaceFilePreview{}, err
	}
	return reader.PreviewWorkspaceFile(ctx, location, file)
}

func (v *Workspaces) WorkspaceFileLink(ctx context.Context, location domain.WorkspaceLocation, file string) (application.WorkspaceFileLink, error) {
	reader, err := v.reader(location)
	if err != nil {
		return application.WorkspaceFileLink{}, err
	}
	return reader.WorkspaceFileLink(ctx, location, file)
}

// OpenWorkspaceFile opens the file one of Agent's links grants: only a
// Managed Work's files are behind such links.
func (v *Workspaces) OpenWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, grant application.WorkspaceFileGrant) (application.OpenedWorkspaceFile, error) {
	if !location.ThroughComputer() {
		return application.OpenedWorkspaceFile{}, application.ErrInvalidWorkspaceFileLink
	}
	if v.managed == nil {
		return application.OpenedWorkspaceFile{}, domain.ErrWorkspaceUnavailable
	}
	return v.managed.OpenWorkspaceFile(ctx, location, grant)
}

// workspaceReader reads the workspace at one kind of location.
type workspaceReader interface {
	WorkspaceView(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceListing, error)
	PreviewWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceFilePreview, error)
	WorkspaceFileLink(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceFileLink, error)
}

// reader selects the reader of a location. A deployment that does not reach
// Managed Computers cannot reach a Managed Work's files.
func (v *Workspaces) reader(location domain.WorkspaceLocation) (workspaceReader, error) {
	if !location.ThroughComputer() {
		return v.storage, nil
	}
	if v.managed == nil {
		return nil, domain.ErrWorkspaceUnavailable
	}
	return v.managed, nil
}
