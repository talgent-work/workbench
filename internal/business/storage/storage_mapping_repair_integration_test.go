//go:build integration

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
)

// One repair run aligns a deliberately misaligned view: a missing link is
// made, a stale file and a foreign file inside a mapping go, and so does
// everything no mapping holds, while the folders above mappings stay.
func TestOneRepairAlignsEveryMappedPath(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:1"))
	token := f.open(inputs)
	for _, name := range []string{"a.md", "b.md"} {
		require.Equal(t, 200, f.upload(inputs.Directory, token, name, name, false).StatusCode)
	}
	require.NoError(t, f.mapAt(inputs, "view/p1/case-1/inputs"))
	empty := fileSpace(named("artifacts", "subject", "case:1"))
	require.NoError(t, f.mapAt(empty, "view/p1/case-1/artifacts"))

	mapped := f.viewOf("view/p1/case-1/inputs")
	require.NoError(t, os.Remove(filepath.Join(mapped, "a.md")))
	require.NoError(t, os.Remove(filepath.Join(mapped, "b.md")))
	require.NoError(t, os.WriteFile(filepath.Join(mapped, "b.md"), []byte("a copy, not the file"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mapped, "foreign.md"), []byte("x"), 0o644))
	require.NoError(t, os.RemoveAll(f.viewOf("view/p1/case-1/artifacts")))
	for _, orphan := range []string{"view/p1/ended/inputs/old.md", "view/p2/x.md", "stray/y.md"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(f.viewOf(orphan)), 0o755))
		require.NoError(t, os.WriteFile(f.viewOf(orphan), []byte("orphan"), 0o644))
	}
	require.NoError(t, os.WriteFile(f.viewOf("view/p1/loose.md"), []byte("loose"), 0o644))

	require.NoError(t, f.app.RepairMappedView(t.Context()))
	f.requireShowsDirectory(inputs, "view/p1/case-1/inputs", "a.md", "b.md")
	require.DirExists(t, f.viewOf("view/p1/case-1/artifacts"), "a mapping without files keeps its place")
	for _, gone := range []string{"view/p1/ended", "view/p2", "stray", "view/p1/loose.md"} {
		require.NoFileExists(t, f.viewOf(gone))
		require.NoDirExists(t, f.viewOf(gone))
	}
	require.NoError(t, f.app.RepairMappedView(t.Context()), "a repair of an aligned view changes nothing")
	f.requireShowsDirectory(inputs, "view/p1/case-1/inputs", "a.md", "b.md")
}

// A repair running while files are written, replaced and deleted, and
// while other Directories are mapped and unmapped, never leaves a stale
// file behind, and the view ends aligned.
func TestRepairRunsAlongsideConcurrentWritesAndMappings(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:2"))
	token := f.open(inputs)
	require.NoError(t, f.mapAt(inputs, "view/p1/case-2"))

	failures := make(chan error, 64)
	var work sync.WaitGroup
	work.Add(2)
	go func() {
		defer work.Done()
		for i := range 20 {
			name := fmt.Sprintf("f%d.md", i%5)
			_, statErr := os.Stat(filepath.Join(f.folder(inputs), name))
			switch {
			case statErr == nil && i%2 == 0:
				failures <- f.app.DeleteFile(t.Context(), at(inputs.Directory, name), token, application.Actor{UserID: "user-1"})
			case statErr == nil:
				failures <- statusError(f.upload(inputs.Directory, token, name, fmt.Sprint(i), true).StatusCode)
			default:
				failures <- statusError(f.upload(inputs.Directory, token, name, fmt.Sprint(i), false).StatusCode)
			}
		}
	}()
	go func() {
		defer work.Done()
		for i := range 10 {
			other := fileSpace(named("inputs", "subject", fmt.Sprintf("other:%d", i)))
			path := fmt.Sprintf("view/p2/other-%d", i)
			failures <- f.mapAt(other, path)
			if i%2 == 0 {
				failures <- f.app.UnmapDirectory(t.Context(), other.Directory, path, viewPrefix)
			}
		}
	}()
	for range 5 {
		require.NoError(t, f.app.RepairMappedView(t.Context()))
	}
	work.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}

	var present []string
	entries, err := os.ReadDir(f.folder(inputs))
	require.NoError(t, err)
	for _, entry := range entries {
		present = append(present, entry.Name())
	}
	f.requireShowsDirectory(inputs, "view/p1/case-2", present...)
	for i := range 10 {
		if i%2 == 0 {
			require.NoDirExists(t, f.viewOf(fmt.Sprintf("view/p2/other-%d", i)))
		} else {
			require.DirExists(t, f.viewOf(fmt.Sprintf("view/p2/other-%d", i)), "a mapping made during a repair stays")
		}
	}
}

func statusError(status int) error {
	if status != 200 {
		return fmt.Errorf("upload answered %d", status)
	}
	return nil
}

// A mapping prefix is a standing path: its folder exists before anything is
// mapped beneath it, and neither unmapping nor a repair removes it emptied.
func TestTheStandingPathStaysForItsVolume(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.app.PrepareMappedView(t.Context()))
	require.DirExists(t, f.viewOf("view"))
	inputs := fileSpace(named("inputs", "subject", "case:3"))
	require.NoError(t, f.mapAt(inputs, "view/p1/case-3"))
	require.NoError(t, f.app.UnmapDirectory(t.Context(), inputs.Directory, "view/p1/case-3", viewPrefix))
	require.NoDirExists(t, f.viewOf("view/p1"))
	require.DirExists(t, f.viewOf("view"), "the last unmapping leaves the standing path")
	require.NoError(t, os.RemoveAll(f.viewOf("view")))
	require.NoError(t, f.app.RepairMappedView(t.Context()))
	require.DirExists(t, f.viewOf("view"), "a repair restores it")
}
