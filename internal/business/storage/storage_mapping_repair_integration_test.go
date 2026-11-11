//go:build integration

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
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

// A repair running while files are written, replaced and deleted never
// leaves a stale file behind, and the view ends aligned.
func TestRepairRunsAlongsideConcurrentWrites(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:2"))
	token := f.open(inputs)
	require.NoError(t, f.mapAt(inputs, "view/p1/case-2"))

	var writes sync.WaitGroup
	writes.Add(1)
	go func() {
		defer writes.Done()
		for i := range 20 {
			name := fmt.Sprintf("f%d.md", i%5)
			if _, err := os.Stat(filepath.Join(f.folder(inputs), name)); err == nil {
				if i%2 == 0 {
					require.NoError(t, f.app.DeleteFile(t.Context(), at(inputs.Directory, name), token, "user-1"))
				} else {
					require.Equal(t, 200, f.upload(inputs.Directory, token, name, fmt.Sprint(i), true).StatusCode)
				}
				continue
			}
			require.Equal(t, 200, f.upload(inputs.Directory, token, name, fmt.Sprint(i), false).StatusCode)
		}
	}()
	for range 5 {
		require.NoError(t, f.app.RepairMappedView(t.Context()))
	}
	writes.Wait()

	var present []string
	entries, err := os.ReadDir(f.folder(inputs))
	require.NoError(t, err)
	for _, entry := range entries {
		present = append(present, entry.Name())
	}
	f.requireShowsDirectory(inputs, "view/p1/case-2", present...)
}
