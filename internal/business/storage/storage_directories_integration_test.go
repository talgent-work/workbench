//go:build integration

package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	applicationtask "github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/business/storage/infrastructure"
)

// harnessState locates an Agent's Harness state, following its
// AgentDefinition: one permanent NAS Directory every Work of it shares.
func harnessState() application.Located {
	return application.Located{Directory: named("claude", "agent", "agent-1"), Backend: domain.BackendNAS, Lifetime: domain.LifetimePermanent}
}

// mountHome mounts a Work's $HOME with the Harness state inside it.
func (f *fixture) mountHome(workID string) []application.MountGrant {
	f.t.Helper()
	grants, err := f.app.AcquireMounts(f.t.Context(), workID, []application.MountRequest{
		{Directory: workDirectory(workID, "home"), MountPoints: []string{".claude"}},
		{Directory: harnessState()},
	})
	require.NoError(f.t, err)
	return grants
}

// Two Works applying the same Locator to the same facts reach one Persistent
// Directory, and each keeps its own Work Directory; a restart reaches the
// same Directories with their content retained.
func TestTwoWorksShareAPersistentDirectoryAndKeepTheirOwnWorkDirectories(t *testing.T) {
	f := newFixture(t)
	first := f.mountHome("work-1")
	second := f.mountHome("work-2")

	require.Equal(t, first[1], second[1], "the same facts locate the same Persistent Directory")
	require.NotEqual(t, first[0], second[0], "each Work has its own Work Directory")
	require.Equal(t, application.MountGrant{ClaimName: "nas-root", SubPath: "directories/agent=agent-1/claude"}, first[1])
	require.Equal(t, application.MountGrant{ClaimName: "nas-root", SubPath: "directories/work=work-1/home"}, first[0])

	require.NoError(t, os.WriteFile(filepath.Join(f.working, first[1].SubPath, "memory.md"), []byte("m"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.working, first[0].SubPath, "scratch"), []byte("s"), 0o644))
	again := f.mountHome("work-1")
	require.Equal(t, first, again, "opening is idempotent")
	require.FileExists(t, filepath.Join(f.working, again[0].SubPath, "scratch"))
	require.FileExists(t, filepath.Join(f.working, second[1].SubPath, "memory.md"), "the other Work sees the shared state")
	require.NoFileExists(t, filepath.Join(f.working, second[0].SubPath, "scratch"), "and never the first Work's own files")

	token, err := f.app.OpenDirectory(t.Context(), harnessState())
	require.NoError(t, err)
	again2, err := f.app.OpenDirectory(t.Context(), application.Located{Directory: harnessState().Directory, Backend: domain.BackendJuiceFS, Lifetime: domain.LifetimeWork})
	require.ErrorIs(t, err, domain.ErrInvalidLocator, "a Work lifetime needs the work condition")
	require.Empty(t, again2)
	moved, err := f.app.OpenDirectory(t.Context(), application.Located{Directory: harnessState().Directory, Backend: domain.BackendJuiceFS, Lifetime: domain.LifetimePermanent})
	require.NoError(t, err)
	require.Equal(t, token, moved, "the first allocation wins")
	require.NoDirExists(t, filepath.Join(f.persistent, juiceFSDirectories, "agent=agent-1"), "it stays on its backend")
}

// An allocation that fails half-way leaves the Directory allocating: nothing
// is mounted, and the next opening completes it with the same Space Token.
func TestAnInterruptedAllocationIsCompletedByTheNextOpening(t *testing.T) {
	f := newFixture(t)
	f.nas.failing.Store(true)
	_, err := f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: workDirectory("work-1", "home")}})
	require.ErrorIs(t, err, errInterrupted, "nothing is granted before the space exists")
	_, err = f.app.OpenDirectory(t.Context(), workDirectory("work-1", "home"))
	require.ErrorIs(t, err, errInterrupted)
	id, err := named("home", "work", "work-1").ID()
	require.NoError(t, err)
	recorded, err := f.directories.Get(t.Context(), id)
	require.NoError(t, err, "the record exists without its space")
	require.Equal(t, domain.DirectoryAllocating, recorded.State)
	require.NoDirExists(t, f.folder(workDirectory("work-1", "home")))

	f.nas.failing.Store(false)
	grants, err := f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: workDirectory("work-1", "home")}})
	require.NoError(t, err)
	require.DirExists(t, filepath.Join(f.working, grants[0].SubPath))
	completed, err := f.directories.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.DirectoryAllocated, completed.State)
	require.Equal(t, recorded.SpaceToken, completed.SpaceToken, "the same Space Token each time")
}

// Every mount point named inside a Directory, and every parent of it,
// belongs to the sandbox user, also where an earlier mount left a directory
// owned by someone else.
func TestMountPointsAndTheirParentsBelongToTheSandboxUser(t *testing.T) {
	f := newFixture(t)
	home := workDirectory("work-1", "home")
	grants, err := f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{
		{Directory: home, MountPoints: []string{".claude", ".config/tool/state", ".config/tool/cache"}},
	})
	require.NoError(t, err)
	root := filepath.Join(f.working, grants[0].SubPath)
	for _, directory := range []string{"", ".claude", ".config", ".config/tool", ".config/tool/state", ".config/tool/cache"} {
		info, err := os.Lstat(filepath.Join(root, directory))
		require.NoError(t, err, directory)
		require.True(t, info.IsDir(), directory)
		require.Equal(t, uint32(f.sandboxUID), info.Sys().(*syscall.Stat_t).Uid, "%q belongs to the sandbox user", directory)
	}

	// A link the sandbox planted where a mount point goes is never followed.
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, "planted")))
	_, err = f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: home, MountPoints: []string{"planted/inner"}}})
	require.Error(t, err)

	_, err = f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: home, MountPoints: []string{"../escape"}}})
	require.ErrorIs(t, err, domain.ErrInvalidMountPoint)
}

// JuiceFS Directories are granted read-only through the JuiceFS claim, laid
// out by owner and name beneath its root.
func TestJuiceFSDirectoriesAreGrantedFromTheirClaim(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:1"))
	grants, err := f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: inputs, ReadOnly: true}})
	require.NoError(t, err)
	require.Equal(t, []application.MountGrant{{ClaimName: "juicefs-inputs", SubPath: "talgent/assets/directories/subject=case:1/inputs", ReadOnly: true}}, grants)
	require.DirExists(t, f.folder(inputs))
	_, err = f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: inputs}, {Directory: inputs}})
	require.ErrorIs(t, err, application.ErrInvalidRequest, "each Directory once")
}

// One reclamation request reclaims every Directory with the Work's lifetime,
// and their content goes only once publications copying from them finish.
// Persistent Directories and every other Work's Directories stay; a reclaimed
// Directory is never mounted again and reads as reclaimed, unlike one never
// opened.
func TestOneReclamationRemovesAWorksDirectoriesAfterPublicationsFinish(t *testing.T) {
	f := newFixture(t)
	artifacts := fileSpace(named("artifacts", "work", "work-1"))
	token := f.open(artifacts)
	home := f.mountHome("work-1")
	other := f.mountHome("work-2")
	layout := f.workspace("work-1", nil)
	f.workspaceFile("work-1", "out.md", "o")

	copying, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		backend := infrastructure.NewDirectoryFiles(infrastructure.DirectoryLayout{Root: f.working, Published: nasDirectories, Private: nasPrivate, SandboxUID: -1})
		id, err := layout.Workspace.ID()
		if err != nil {
			finished <- err
			return
		}
		finished <- backend.CopyPublicationSource(context.Background(), id, "out.md", func(io.Reader) error {
			close(copying)
			<-release
			return nil
		})
	}()
	<-copying
	require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-1"))
	require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-1"), "repeating it changes nothing")
	f.removals()
	workspace := f.folder(workDirectory("work-1", "workspace"))
	require.DirExists(t, workspace, "a copy in progress keeps its source")
	require.NoDirExists(t, filepath.Join(f.working, home[0].SubPath), "a Directory nothing copies from goes at once")
	require.Equal(t, 1, f.queue.size(applicationtask.RemoveReclaimedContentType), "the copied Directory's removal waits and retries")
	close(release)
	require.NoError(t, <-finished)
	f.removals()
	require.Zero(t, f.queue.size(applicationtask.RemoveReclaimedContentType))

	require.NoDirExists(t, workspace)
	require.NoDirExists(t, filepath.Join(f.working, home[0].SubPath), "every Directory with the Work's lifetime goes")
	require.NoDirExists(t, filepath.Dir(filepath.Join(f.working, home[0].SubPath)), "and its owner's folder with the last of them")
	require.DirExists(t, filepath.Join(f.working, home[1].SubPath), "the Persistent Directory stays")
	require.DirExists(t, filepath.Join(f.working, other[0].SubPath), "another Work's Directories stay")
	require.DirExists(t, f.folder(artifacts), "a permanent Directory the Work names stays")
	entries, err := os.ReadDir(filepath.Join(f.working, nasPrivate, "reclaimed"))
	require.NoError(t, err)
	require.Empty(t, entries, "nothing removed is left behind")

	_, err = f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: workDirectory("work-1", "home")}})
	require.ErrorIs(t, err, domain.ErrDirectoryReclaimed, "a reclaimed Directory is never mounted or recreated")
	require.NoDirExists(t, filepath.Join(f.working, home[0].SubPath))
	_, err = f.app.WorkspaceView(t.Context(), layout, "")
	require.ErrorIs(t, err, domain.ErrDirectoryReclaimed, "it reads as reclaimed")
	_, err = f.app.WorkspaceView(t.Context(), application.Layout{Workspace: named("workspace", "work", "never-started")}, "")
	require.ErrorIs(t, err, domain.ErrDirectoryNotOpen, "unlike one never opened")
	_, err = f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: token, WorkID: "work-1", Workspace: &layout, Path: "out.md"}, "out.md", false)
	require.ErrorIs(t, err, domain.ErrDirectoryReclaimed, "a publication after reclamation finds no source")
}

// A permanent Directory refuses reclamation even when its owner names the
// Work: only the Work's lifetime admits it.
func TestAPermanentDirectoryRefusesReclamation(t *testing.T) {
	f := newFixture(t)
	f.open(fileSpace(named("artifacts", "work", "work-1")))
	id, err := named("artifacts", "work", "work-1").ID()
	require.NoError(t, err)
	directory, err := f.directories.Get(t.Context(), id)
	require.NoError(t, err)
	require.ErrorIs(t, directory.Reclaim(), domain.ErrPermanentDirectory)
	require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-1"))
	directory, err = f.directories.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.DirectoryAllocated, directory.State)
	require.Zero(t, f.queue.size(applicationtask.RemoveReclaimedContentType))
}

// Owners are laid out as the published originals layout writes them.
func TestDirectoriesAreLaidOutByOwnerAndName(t *testing.T) {
	f := newFixture(t)
	odd := fileSpace(named("inputs", "person", "a/b,c", "project", "p 1"))
	f.open(odd)
	require.DirExists(t, filepath.Join(f.persistent, "assets/directories", "person=a%2Fb%2Cc,project=p%201", "inputs"))
	tooLong := fileSpace(named("inputs", "subject", strings.Repeat("/", 100)))
	_, err := f.app.OpenDirectory(t.Context(), tooLong)
	require.ErrorIs(t, err, domain.ErrInvalidLocator, "an owner that is not one path element is refused")
}

// A Directory reclaimed while its space is being allocated is not left
// recreated: the opening fails as reclaimed and queues its removal again.
func TestADirectoryReclaimedDuringItsAllocationIsRemovedAgain(t *testing.T) {
	f := newFixture(t)
	f.nas.meanwhile = func() {
		f.nas.meanwhile = nil
		require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-1"))
		f.removals()
	}
	_, err := f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: workDirectory("work-1", "home")}})
	require.ErrorIs(t, err, domain.ErrDirectoryReclaimed)
	f.removals()
	require.NoDirExists(t, f.folder(workDirectory("work-1", "home")), "a reclaimed Directory is never recreated")
}

// A Work mounts no Directory another Work owns.
func TestAWorkMountsNoOtherWorksDirectory(t *testing.T) {
	f := newFixture(t)
	_, err := f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: workDirectory("work-2", "home")}})
	require.ErrorIs(t, err, application.ErrInvalidRequest)
	require.NoDirExists(t, f.folder(workDirectory("work-2", "home")))
}
