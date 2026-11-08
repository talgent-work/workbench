package domain

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// ErrInvalidWorkLayout means a Work Layout lacks a root, has two mounts at
// one path, a mount outside its roots, a writable mount following the
// Subject, or an invalid Locator.
var ErrInvalidWorkLayout = fmt.Errorf("%w: invalid Work Layout", ErrInvalidAgentDefinition)

// The roots of every Work Layout (Layout roots): the Work's HOME and its
// Session Workspace.
const (
	HomePath      = "$HOME"
	WorkspacePath = "/workspace"
	// inputsPath is where a Work reads its located inputs; a Managed
	// Computer copies them to the same place of its workspace.
	inputsPath = WorkspacePath + "/inputs"
)

// LayoutMount is one mount of a Work Layout: a path, an access mode and one
// or more Locators, the first that locates winning (Locator order). The
// Harness state mount names no path and its Locator no name: both come from
// the Work's Agent Harness (Harness state placement).
type LayoutMount struct {
	Path         string
	ReadOnly     bool
	Locators     []DirectoryLocator
	HarnessState bool
}

// WorkLayout is an AgentDefinition's tree of mounts for its Works, rooted at
// $HOME and the Session Workspace, with at most one mount per path.
type WorkLayout struct {
	Mounts []LayoutMount
}

// DefaultWorkLayout is the product default every new AgentDefinition starts
// with (Default layout): its roots as NAS Work Directories following the
// Work, the Harness state following the AgentDefinition, and read-only inputs,
// following the Subject else the Work, and Project knowledge on JuiceFS.
func DefaultWorkLayout() WorkLayout {
	permanent := func(condition LocatorCondition, name string) DirectoryLocator {
		return DirectoryLocator{Conditions: []LocatorCondition{condition}, Name: name, Backend: BackendJuiceFS, Lifetime: LifetimePermanent}
	}
	return WorkLayout{Mounts: []LayoutMount{
		{Path: HomePath, Locators: []DirectoryLocator{workDirectory("home")}},
		{Path: WorkspacePath, Locators: []DirectoryLocator{workDirectory("workspace")}},
		{HarnessState: true, Locators: []DirectoryLocator{{Conditions: []LocatorCondition{ConditionAgentDefinition}, Backend: BackendNAS, Lifetime: LifetimePermanent}}},
		{Path: inputsPath, ReadOnly: true, Locators: []DirectoryLocator{permanent(ConditionSubject, "inputs"), permanent(ConditionWork, "inputs")}},
		{Path: WorkspacePath + "/knowledge", ReadOnly: true, Locators: []DirectoryLocator{permanent(ConditionProject, "knowledge")}},
	}}
}

// workDirectory is a root's Locator: a NAS Work Directory following the Work.
func workDirectory(name string) DirectoryLocator {
	return DirectoryLocator{Conditions: []LocatorCondition{ConditionWork}, Name: name, Backend: BackendNAS, Lifetime: LifetimeWork}
}

// Clone copies the layout.
func (l WorkLayout) Clone() WorkLayout {
	mounts := make([]LayoutMount, len(l.Mounts))
	for i, mount := range l.Mounts {
		mount.Locators = slices.Clone(mount.Locators)
		for j := range mount.Locators {
			mount.Locators[j] = mount.Locators[j].clone()
		}
		mounts[i] = mount
	}
	return WorkLayout{Mounts: mounts}
}

// Validate is checked whenever its AgentDefinition is saved: both roots
// read-write as NAS Work Directories following the Work, every other path
// beneath a root, one mount per path (One mount per path), at most one
// Harness state mount, valid Locators, and every mount following the Subject
// read-only (Subject mounts read-only).
func (l WorkLayout) Validate() error {
	paths := make([]string, 0, len(l.Mounts))
	harnessStates := 0
	for _, mount := range l.Mounts {
		if err := mount.validate(); err != nil {
			return err
		}
		if mount.HarnessState {
			harnessStates++
			continue
		}
		if slices.Contains(paths, mount.Path) {
			return fmt.Errorf("%w: two mounts at %s", ErrInvalidWorkLayout, mount.Path)
		}
		paths = append(paths, mount.Path)
	}
	if harnessStates > 1 {
		return fmt.Errorf("%w: more than one Harness state mount", ErrInvalidWorkLayout)
	}
	for _, root := range []string{HomePath, WorkspacePath} {
		if !slices.Contains(paths, root) {
			return fmt.Errorf("%w: no mount at %s", ErrInvalidWorkLayout, root)
		}
	}
	return nil
}

func (m LayoutMount) validate() error {
	if len(m.Locators) == 0 {
		return fmt.Errorf("%w: a mount has a Locator", ErrInvalidWorkLayout)
	}
	if m.HarnessState {
		if m.Path != "" || len(m.Locators) != 1 || m.Locators[0].Name != "" {
			return fmt.Errorf("%w: the Harness state mount takes its path and name from the Agent Harness", ErrInvalidWorkLayout)
		}
		named := m.Locators[0].clone()
		named.Name = "harness"
		if err := named.Validate(); err != nil {
			return errors.Join(ErrInvalidWorkLayout, err)
		}
		return m.validateAccess()
	}
	root, _, ok := splitLayoutPath(m.Path)
	if !ok {
		return fmt.Errorf("%w: path %q is not beneath $HOME or /workspace", ErrInvalidWorkLayout, m.Path)
	}
	for _, locator := range m.Locators {
		if err := locator.Validate(); err != nil {
			return errors.Join(ErrInvalidWorkLayout, err)
		}
	}
	if m.Path == root {
		locators := m.Locators
		if m.ReadOnly || len(locators) != 1 || locators[0].Backend != BackendNAS || locators[0].Lifetime != LifetimeWork || !slices.Equal(locators[0].Conditions, []LocatorCondition{ConditionWork}) {
			return fmt.Errorf("%w: %s is a read-write NAS Work Directory following the Work", ErrInvalidWorkLayout, root)
		}
	}
	return m.validateAccess()
}

func (m LayoutMount) validateAccess() error {
	if !m.ReadOnly && slices.ContainsFunc(m.Locators, func(l DirectoryLocator) bool { return l.follows(ConditionSubject) }) {
		return fmt.Errorf("%w: a mount following the Subject is read-only", ErrInvalidWorkLayout)
	}
	return nil
}

// splitLayoutPath splits a layout path into its root and the clean path
// relative to it, "" for the root itself.
func splitLayoutPath(value string) (root, relative string, ok bool) {
	for _, root := range []string{HomePath, WorkspacePath} {
		if value == root {
			return root, "", true
		}
		rest, found := strings.CutPrefix(value, root+"/")
		if found && rest != "" && path.Clean(rest) == rest && !path.IsAbs(rest) && rest != "." && rest != ".." &&
			!strings.HasPrefix(rest, "../") && !strings.ContainsAny(rest, "\\\x00") {
			return root, rest, true
		}
	}
	return "", "", false
}

// within reports the path of inner relative to outer when inner lies strictly
// beneath it.
func within(outer, inner string) (string, bool) {
	relative, found := strings.CutPrefix(inner, outer+"/")
	return relative, found && relative != ""
}
