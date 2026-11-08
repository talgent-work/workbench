package execution

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// StorageWorkspaces is Storage's side of a Work's workspace: the Session
// Workspace's views and links.
type StorageWorkspaces interface {
	WorkspaceView(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceListing, error)
	PreviewWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceFilePreview, error)
	WorkspaceFileLink(ctx context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceFileLink, error)
}

// Workspaces reads a Work's workspace at the location its AgentWork names, as
// Environment runs it where it runs: a directory on a Managed Computer through
// the relay, any other location from Storage's Session Workspace. This is the
// one place that tells the two apart. Only Managed Works' files are served
// behind Agent's own links.
type Workspaces struct {
	cloud   StorageWorkspaces
	managed *ManagedWorkspaces
}

var _ application.WorkspacePort = (*Workspaces)(nil)

// NewWorkspaces reads Cloud Works through cloud and, when the deployment
// reaches Managed Computers, Managed Works through managed.
func NewWorkspaces(cloud StorageWorkspaces, managed *ManagedWorkspaces) (*Workspaces, error) {
	if cloud == nil {
		return nil, errors.New("agent: Storage's workspaces are required")
	}
	return &Workspaces{cloud: cloud, managed: managed}, nil
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
		return v.cloud, nil
	}
	if v.managed == nil {
		return nil, domain.ErrWorkspaceUnavailable
	}
	return v.managed, nil
}
