// Package nativetest gates the integration cases that drive the native
// binaries the sandbox ships, such as Claude Code and execd.
package nativetest

import (
	"os"
	"strings"
	"testing"
)

// Prefix starts the name of every native case: CI's native run (make
// test-native) selects the cases by it, so a native case named otherwise
// would never run there.
const Prefix = "TestNative"

// Require returns the named environment values in order and skips the case
// when any is unset. A case whose top-level name lacks Prefix fails, so it
// is renamed before CI can leave it unrun.
func Require(t testing.TB, names ...string) []string {
	t.Helper()
	if top, _, _ := strings.Cut(t.Name(), "/"); !strings.HasPrefix(top, Prefix) {
		t.Fatalf("%s reads %s, so its name must start with %s for make test-native to run it", top, strings.Join(names, ", "), Prefix)
	}
	values := make([]string, len(names))
	var missing []string
	for i, name := range names {
		values[i] = os.Getenv(name)
		if values[i] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Skipf("%s required", strings.Join(missing, ", "))
	}
	return values
}
