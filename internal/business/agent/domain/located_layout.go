package domain

import (
	"fmt"
	"slices"
	"strings"
)

// LocatedMount is one Directory a Work's layout located, with the path where
// it is mounted and its access mode.
type LocatedMount struct {
	Path      string
	ReadOnly  bool
	Directory LocatedDirectory
}

// LocatedLayout is what a Work's Layout located from its facts at creation,
// fixed for its life (Fixed layout): the Directories its sandbox mounts, the
// Directory it reads its inputs from and its publication target. A Work on a
// Managed Computer mounts no Directory (Managed workspace), so it keeps only
// its inputs, which its machine copies, and its publication target.
type LocatedLayout struct {
	Mounts []LocatedMount
	// Inputs is the Directory located at /workspace/inputs; nil when that
	// mount located nothing.
	Inputs *LocatedDirectory
	// PublicationTarget is the File Space the Work publishes Artifacts into
	// (Publication target).
	PublicationTarget LocatedDirectory
}

// Clone copies the layout.
func (l LocatedLayout) Clone() LocatedLayout {
	mounts := make([]LocatedMount, len(l.Mounts))
	for i, mount := range l.Mounts {
		mount.Directory = mount.Directory.clone()
		mounts[i] = mount
	}
	l.Mounts = mounts
	if l.Inputs != nil {
		inputs := l.Inputs.clone()
		l.Inputs = &inputs
	}
	l.PublicationTarget = l.PublicationTarget.clone()
	return l
}

// locateLayout applies the definition's Work Layout to the Work's facts
// (Layout location): each mount takes the first of its Locators that
// locates, a mount that locates nothing is left out, and the Harness state
// mount takes its path and Directory name from the Agent Harness (Harness
// state placement), refused when another mount holds that path or one inside it
// (Located layout validity). The publication target follows the File Home,
// else the Work.
func locateLayout(layout WorkLayout, harness AgentHarness, facts LocatorFacts, managed bool) (LocatedLayout, error) {
	if err := layout.Validate(); err != nil {
		return LocatedLayout{}, err
	}
	var located LocatedLayout
	statePath := harness.StateMountPath()
	for _, mount := range layout.Mounts {
		locators, mountPath := mount.Locators, mount.Path
		if mount.HarnessState {
			if slices.ContainsFunc(layout.Mounts, func(other LayoutMount) bool { return !other.HarnessState && collides(statePath, other.Path) }) {
				return LocatedLayout{}, fmt.Errorf("%w: the Agent Harness's state path %s holds another mount", ErrInvalidWork, statePath)
			}
			named := locators[0].clone()
			named.Name = harness.StateDirectory
			locators, mountPath = []DirectoryLocator{named}, statePath
		}
		for _, locator := range locators {
			if directory, ok := locator.Apply(facts); ok {
				located.Mounts = append(located.Mounts, LocatedMount{Path: mountPath, ReadOnly: mount.ReadOnly, Directory: directory})
				break
			}
		}
	}
	for _, mount := range located.Mounts {
		if mount.Path == inputsPath {
			inputs := mount.Directory.clone()
			located.Inputs = &inputs
		}
	}
	// Every Work has its own identity, so the target is always located;
	// refusing otherwise keeps a Work from ever lacking one.
	target, ok := publicationTarget(facts)
	if !ok {
		return LocatedLayout{}, fmt.Errorf("%w: no publication target locates", ErrInvalidWork)
	}
	located.PublicationTarget = target
	if managed {
		located.Mounts = nil
	}
	return located, located.validate()
}

// collides reports whether a mount at path would take the Harness state
// path: the same path, or one nested inside it, whose Directory would hide
// part of the Harness's state.
func collides(statePath, path string) bool {
	_, nested := within(statePath, path)
	return path == statePath || nested
}

// publicationTarget is `{File Home}, artifacts`, or `{Work}, artifacts` for a
// Work without a File Home: a permanent File Space on JuiceFS (Publication
// target).
func publicationTarget(facts LocatorFacts) (LocatedDirectory, bool) {
	for _, condition := range []LocatorCondition{ConditionFileHome, ConditionWork} {
		locator := DirectoryLocator{Conditions: []LocatorCondition{condition}, Name: "artifacts", Backend: BackendJuiceFS, Lifetime: LifetimePermanent}
		if directory, ok := locator.Apply(facts); ok {
			return directory, true
		}
	}
	return LocatedDirectory{}, false
}

// validate keeps a located layout as locating could fix it: valid
// Directories, each path once, and a publication target.
func (l LocatedLayout) validate() error {
	paths := make([]string, 0, len(l.Mounts))
	for _, mount := range l.Mounts {
		if _, _, ok := splitLayoutPath(mount.Path); !ok || slices.Contains(paths, mount.Path) {
			return fmt.Errorf("%w: invalid located mount %q", ErrInvalidWork, mount.Path)
		}
		if err := mount.Directory.validate(); err != nil {
			return err
		}
		paths = append(paths, mount.Path)
	}
	if l.Inputs != nil {
		if err := l.Inputs.validate(); err != nil {
			return err
		}
	}
	return l.PublicationTarget.validate()
}

// DirectoryMount is one Directory a start asks Storage to grant: its access
// mode and the mount points of the mounts nested inside it.
type DirectoryMount struct {
	Directory   LocatedDirectory
	ReadOnly    bool
	MountPoints []string
}

// MountRequests are the Mount Grants a start requests: exactly one per
// located Directory, in layout order, each naming the paths of the mounts
// nested directly inside it (Sandbox mounts).
func (l LocatedLayout) MountRequests() []DirectoryMount {
	requests := make([]DirectoryMount, 0, len(l.Mounts))
	for i, mount := range l.Mounts {
		request := DirectoryMount{Directory: mount.Directory.clone(), ReadOnly: mount.ReadOnly}
		for j, inner := range l.Mounts {
			if relative, ok := within(mount.Path, inner.Path); ok && l.parent(j) == i {
				request.MountPoints = append(request.MountPoints, relative)
			}
		}
		requests = append(requests, request)
	}
	return requests
}

// parent is the index of the deepest located mount strictly containing the
// one at index, -1 for none.
func (l LocatedLayout) parent(index int) int {
	parent, depth := -1, -1
	for i, mount := range l.Mounts {
		if _, ok := within(mount.Path, l.Mounts[index].Path); ok && len(mount.Path) > depth {
			parent, depth = i, len(mount.Path)
		}
	}
	return parent
}

// MountedDirectory is a Directory mounted inside a Work Directory, at its
// path relative to it.
type MountedDirectory struct {
	Path      string
	Directory Directory
}

// WorkspaceLayout is the Session Workspace as the Work's sandbox sees it:
// the Work Directory and the Directories mounted inside it (Workspace
// naming).
type WorkspaceLayout struct {
	Workspace Directory
	Mounts    []MountedDirectory
}

// Workspace is the Session Workspace's layout; false for a Work that mounts
// none, such as a Work on a Managed Computer.
func (l LocatedLayout) Workspace() (WorkspaceLayout, bool) {
	var layout WorkspaceLayout
	found := false
	for _, mount := range l.Mounts {
		if mount.Path == WorkspacePath {
			layout.Workspace, found = mount.Directory.Directory.clone(), true
		} else if relative, ok := within(WorkspacePath, mount.Path); ok {
			layout.Mounts = append(layout.Mounts, MountedDirectory{Path: relative, Directory: mount.Directory.Directory.clone()})
		}
	}
	return layout, found
}

// SandboxPath is where a layout path appears in a sandbox whose HOME is home.
func SandboxPath(layoutPath, home string) string {
	if rest, ok := strings.CutPrefix(layoutPath, HomePath); ok {
		return home + rest
	}
	return layoutPath
}
