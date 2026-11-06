package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
)

// Overlapping replacements of one owner run one at a time: each switches in
// its whole content, a read through Storage sees one whole content, and the
// space ends with exactly the last switched content.
func TestOverlappingReplacementsEachSwitchInWholeContent(t *testing.T) {
	root := t.TempDir()
	spaces := NewReferenceSpaces(root)
	content := func(n int) []application.ReferenceFile {
		mark := []byte(fmt.Sprint(n))
		return []application.ReferenceFile{{Path: "index.md", Content: mark}, {Path: "pages/a.md", Content: mark}, {Path: "pages/b.md", Content: mark}}
	}
	var wg sync.WaitGroup
	for n := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			require.NoError(t, spaces.Replace(t.Context(), "project-1", content(n)))
		}()
		go func() {
			defer wg.Done()
			files, err := spaces.ReadCurrent(t.Context(), "project-1", nil)
			require.NoError(t, err)
			if len(files) == 0 {
				return
			}
			require.Len(t, files, 3)
			for _, file := range files {
				require.Equal(t, files[0].Content, file.Content, "one whole content")
			}
		}()
	}
	wg.Wait()

	current, err := spaces.ReadCurrent(t.Context(), "project-1", nil)
	require.NoError(t, err)
	require.Len(t, current, 3)
	contents, err := os.ReadDir(filepath.Join(root, referencesDirectory, "project-1", referenceContents))
	require.NoError(t, err)
	require.Len(t, contents, 1, "no earlier content stays")
	visible, err := os.ReadFile(filepath.Join(root, referencesDirectory, "project-1", "pages", "a.md"))
	require.NoError(t, err)
	require.Equal(t, string(current[0].Content), strings.TrimSpace(string(visible)))
}
