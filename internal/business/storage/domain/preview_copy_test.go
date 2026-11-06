package domain

import (
	"errors"
	"strings"
	"testing"
)

// A Preview Copy is keyed by one lower-case SHA-256 digest, a single path
// element.
func TestPreviewKey(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	if _, err := NewPreviewKey(digest); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", strings.ToUpper(digest), digest[:62], "../" + digest[3:], digest + "00"} {
		if _, err := NewPreviewKey(bad); !errors.Is(err, ErrInvalidPreviewKey) {
			t.Fatalf("%q accepted", bad)
		}
	}
}
