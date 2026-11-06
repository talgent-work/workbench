package infrastructure

import (
	"embed"
	"testing"
)

// The fixtures are embedded so a test binary run from another directory, as
// CI does across UID boundaries, still finds them.
//
//go:embed testdata/preview
var previewFixtures embed.FS

func previewFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := previewFixtures.ReadFile("testdata/preview/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
