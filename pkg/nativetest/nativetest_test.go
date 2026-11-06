package nativetest

import "testing"

// recorder captures what Require does to a case without ending this test.
type recorder struct {
	testing.TB
	name           string
	fatal, skipped bool
}

func (r *recorder) Helper()               {}
func (r *recorder) Name() string          { return r.name }
func (r *recorder) Fatalf(string, ...any) { r.fatal = true; panic(r) }
func (r *recorder) Skipf(string, ...any)  { r.skipped = true; panic(r) }

func run(name string, env ...string) (r *recorder, values []string) {
	r = &recorder{name: name}
	defer func() {
		if p := recover(); p != nil && p != r {
			panic(p)
		}
	}()
	return r, Require(r, env...)
}

func TestRequireSelectsNativeCasesByName(t *testing.T) {
	t.Setenv("NATIVETEST_SET", "/bin/claude")
	if r, values := run("TestNativeLaunch/defaults", "NATIVETEST_SET"); r.fatal || r.skipped || values[0] != "/bin/claude" {
		t.Fatalf("a named native case gets its binary: %+v %v", r, values)
	}
	if r, _ := run("TestNativeLaunch", "NATIVETEST_SET", "NATIVETEST_UNSET"); r.fatal || !r.skipped {
		t.Fatalf("a case missing a binary is skipped: %+v", r)
	}
	if r, _ := run("TestLaunchesNative", "NATIVETEST_UNSET"); !r.fatal {
		t.Fatalf("a native case named otherwise fails, even without its binary: %+v", r)
	}
}
