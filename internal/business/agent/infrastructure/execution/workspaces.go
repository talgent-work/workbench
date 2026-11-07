package execution

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// StorageWorkspaces is Storage's side of a Work's workspace: the Session
// Workspace's views and links, and its reclamation.
type StorageWorkspaces interface {
	WorkspaceView(ctx context.Context, workspace application.ViewedWorkspace, path string) (application.WorkspaceListing, error)
	PreviewWorkspaceFile(ctx context.Context, workspace application.ViewedWorkspace, path string) (application.WorkspaceFilePreview, error)
	WorkspaceFileLink(ctx context.Context, workspace application.ViewedWorkspace, path string) (application.WorkspaceFileLink, error)
	RequestWorkspaceReclamation(ctx context.Context, workID, workspaceReference string) error
}

// Workspaces reads a Work's workspace where its files are, as Environment
// starts it where it runs: a Work whose environment the Managed realization
// opened has its files in its directory on its machine, read through the
// relay while the machine is reachable; any other Work's workspace is
// Storage's. A Work's realization is fixed by its first start, so the
// allocation's recorded realization decides, never a guess. Only Managed
// Works' files are served behind Agent's own links, and a Work's reclamation
// is always Storage's.
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

func (v *Workspaces) WorkspaceView(ctx context.Context, workspace application.ViewedWorkspace, dir string) (application.WorkspaceListing, error) {
	return readWhere(ctx, v, workspace, func(spec managedSpecification) (application.WorkspaceListing, error) {
		return v.managed.view(ctx, spec, workspace.WorkID, dir)
	}, func() (application.WorkspaceListing, error) { return v.cloud.WorkspaceView(ctx, workspace, dir) })
}

func (v *Workspaces) PreviewWorkspaceFile(ctx context.Context, workspace application.ViewedWorkspace, file string) (application.WorkspaceFilePreview, error) {
	return readWhere(ctx, v, workspace, func(spec managedSpecification) (application.WorkspaceFilePreview, error) {
		return v.managed.preview(ctx, spec, workspace.WorkID, file)
	}, func() (application.WorkspaceFilePreview, error) {
		return v.cloud.PreviewWorkspaceFile(ctx, workspace, file)
	})
}

func (v *Workspaces) WorkspaceFileLink(ctx context.Context, workspace application.ViewedWorkspace, file string) (application.WorkspaceFileLink, error) {
	return readWhere(ctx, v, workspace, func(spec managedSpecification) (application.WorkspaceFileLink, error) {
		return v.managed.downloadLink(ctx, spec, workspace.WorkID, file)
	}, func() (application.WorkspaceFileLink, error) { return v.cloud.WorkspaceFileLink(ctx, workspace, file) })
}

// LinkedWork is the Work a link grants a read of. Without Managed Computers
// Agent issues no link, so none is valid.
func (v *Workspaces) LinkedWork(token string) (string, error) {
	if v.managed == nil {
		return "", application.ErrInvalidWorkspaceFileLink
	}
	return v.managed.LinkedWork(token)
}

// OpenLink opens the file a link grants. ErrWorkspaceUnavailable means the
// machine is not reachable now.
func (v *Workspaces) OpenLink(ctx context.Context, token string) (application.OpenedWorkspaceFile, error) {
	if v.managed == nil {
		return application.OpenedWorkspaceFile{}, application.ErrInvalidWorkspaceFileLink
	}
	return v.managed.OpenLink(ctx, token)
}

func (v *Workspaces) RequestWorkspaceReclamation(ctx context.Context, workID, workspaceReference string) error {
	return v.cloud.RequestWorkspaceReclamation(ctx, workID, workspaceReference)
}

// readWhere reads the Work's workspace on its machine when the Managed
// realization opened its environment, and its Session Workspace in Storage
// otherwise. Any other Work, such as a project-scoped definition's or a Managed Work
// whose directory was never opened, has nothing to read yet.
func readWhere[T any](ctx context.Context, v *Workspaces, workspace application.ViewedWorkspace, managed func(managedSpecification) (T, error), cloud func() (T, error)) (T, error) {
	var zero T
	if v.managed != nil {
		spec, opened, err := v.managed.opened(ctx, workspace.WorkID)
		if err != nil {
			return zero, err
		}
		if opened {
			return managed(spec)
		}
	}
	if workspace.Reference == "" {
		return zero, application.ErrWorkspaceNotCreated
	}
	return cloud()
}
