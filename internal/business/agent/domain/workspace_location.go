package domain

import (
	"errors"
	"fmt"
	"slices"
)

var (
	// ErrWorkspaceNotCreated means the Work has no workspace to read yet: a
	// Cloud Work without a Session Workspace, or a Managed Work whose machine
	// never opened its directory.
	ErrWorkspaceNotCreated = errors.New("agent: the workspace has not been created")
	// ErrWorkspaceUnavailable means the Work's files stay on its Managed
	// Computer, which is not reachable now: a Disconnection.
	ErrWorkspaceUnavailable = errors.New("agent: the Computer is not reachable")
)

// WorkspaceLocation is where a Work's files are read: its Session Workspace
// in Storage, with what its sandbox mounts inside it, on a Cloud; its
// directory on its Managed Computer, read through that Computer.
type WorkspaceLocation struct {
	WorkID string
	// ComputerID is the Managed Computer that holds the Work's directory;
	// empty on a Cloud.
	ComputerID string
	// SessionWorkspace, InputSource and ReadOnlyMounts are the Session
	// Workspace and the spaces mounted inside it; empty on a Managed
	// Computer.
	SessionWorkspace string
	InputSource      WorkInputSource
	ReadOnlyMounts   []ReadOnlyMount
}

// ThroughComputer reports whether the files are read through the Work's
// Managed Computer rather than from Storage.
func (l WorkspaceLocation) ThroughComputer() bool { return l.ComputerID != "" }

// LocateWorkspace names where the Work's files are read (Managed workspace,
// Managed workspace reading): a Managed Work's directory through its
// Computer, refused during a Disconnection; a Cloud Work's Session
// Workspace, absent for a Work that has none.
func (s *AgentWork) LocateWorkspace() (WorkspaceLocation, error) {
	switch s.ComputerKind {
	case ComputerManaged:
		if s.Disconnected() {
			return WorkspaceLocation{}, ErrWorkspaceUnavailable
		}
		return WorkspaceLocation{WorkID: s.ID, ComputerID: s.ComputerID}, nil
	case ComputerCloud:
		if s.WorkspaceReference == "" {
			return WorkspaceLocation{}, ErrWorkspaceNotCreated
		}
		return WorkspaceLocation{WorkID: s.ID, SessionWorkspace: s.WorkspaceReference, InputSource: s.InputSource, ReadOnlyMounts: slices.Clone(s.ReadOnlyMounts)}, nil
	}
	return WorkspaceLocation{}, fmt.Errorf("%w: the Work's Computer kind is unknown", ErrInvalidWork)
}
