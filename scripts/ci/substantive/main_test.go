package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo builds a git repository with a base and a head commit and returns the
// two revisions.
func repo(t *testing.T, base, head map[string]string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(files map[string]string) string {
		for path, content := range files {
			full := filepath.Join(dir, path)
			if content == "" {
				_ = os.Remove(full)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", "-A")
		run("commit", "-q", "--allow-empty", "-m", "c")
		return run("rev-parse", "HEAD")
	}
	run("init", "-q")
	return dir, commit(base), commit(head)
}

const goBase = "package p\n\n// Old comment.\ntype T struct {\n\tA string `json:\"a\"`\n}\n\nfunc F() int { return 1 }\n"

func TestOnlySubstantiveChangesAreReported(t *testing.T) {
	dir, base, head := repo(t, map[string]string{
		"comment.go":                     goBase,
		"code.go":                        goBase,
		"tag.go":                         goBase,
		"deleted.go":                     goBase,
		"broken.go":                      goBase,
		"packages/contracts/src/a_pb.ts": "/**\n * Old.\n */\nexport const a = \"x//y\"; // trailing\n",
		"packages/contracts/src/b_pb.ts": "export const b = \"x\";\n",
		"apps/web/src/c.ts":              "export const c = 1;\n",
		"docs/readme.md":                 "old\n",
	}, map[string]string{
		// Comments rewritten and fields realigned: no change to the compiled code.
		"comment.go": "package p\n\n// New comment, longer.\ntype T struct {\n\tA string `json:\"a\"` // documented\n}\n\n// F returns one.\nfunc F() int {\n\treturn 1\n}\n",
		"code.go":    strings.Replace(goBase, "return 1", "return 2", 1),
		"tag.go":     strings.Replace(goBase, `json:"a"`, `json:"b"`, 1),
		"deleted.go": "",
		"broken.go":  "package p\nfunc (",
		"added.go":   "package p\n",
		// Generated TS: only comments and layout differ; a "//" inside a string is code.
		"packages/contracts/src/a_pb.ts": "/**\n * New, longer.\n */\nexport const a =\n  \"x//y\";\n",
		"packages/contracts/src/b_pb.ts": "export const b = \"x// y\";\n",
		// Hand-written TS is always a change: it may hold regex literals the lexer cannot read.
		"apps/web/src/c.ts": "// note\nexport const c = 1;\n",
		"docs/readme.md":    "new\n",
	})
	files := []string{"comment.go", "code.go", "tag.go", "deleted.go", "broken.go", "added.go", "packages/contracts/src/a_pb.ts", "packages/contracts/src/b_pb.ts", "apps/web/src/c.ts", "docs/readme.md"}

	got, err := substantive(dir, base, head, files)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"code.go", "tag.go", "deleted.go", "broken.go", "added.go", "packages/contracts/src/b_pb.ts", "apps/web/src/c.ts", "docs/readme.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("substantive changes\n got: %v\nwant: %v", got, want)
	}
}
