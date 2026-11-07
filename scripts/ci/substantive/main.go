// Command substantive filters a list of changed files down to the ones whose
// change can alter a build, so a change to comments or layout alone does not
// rebuild an image.
//
// Usage: git diff --name-only BASE..HEAD | go run ./scripts/ci/substantive BASE HEAD
//
// A file is reported unless it provably compiles to the same thing at both
// revisions:
//   - Go source is compared after parsing without comments and printing back.
//   - Generated TypeScript under packages/contracts/src/ is compared with comments and layout
//     removed by a lexer for its fixed protobuf-es output. Hand-written
//     TypeScript may hold regular-expression literals that lexer cannot read,
//     so it always counts as changed.
//
// A file added, deleted or unparsable at either revision is always reported.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: substantive BASE HEAD < changed-files")
		os.Exit(2)
	}
	var files []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			files = append(files, line)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	changed, err := substantive(".", os.Args[1], os.Args[2], files)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, file := range changed {
		fmt.Println(file)
	}
}

func substantive(dir, base, head string, files []string) ([]string, error) {
	var changed []string
	for _, file := range files {
		same, err := equivalent(dir, base, head, file)
		if err != nil {
			return nil, err
		}
		if !same {
			changed = append(changed, file)
		}
	}
	return changed, nil
}

func equivalent(dir, base, head, file string) (bool, error) {
	var normalize func([]byte) ([]byte, bool)
	switch {
	case strings.HasSuffix(file, ".go"):
		normalize = goCode
	case strings.HasPrefix(file, "packages/contracts/src/") && strings.HasSuffix(file, ".ts"):
		normalize = generatedTSCode
	default:
		return false, nil
	}
	before, ok, err := show(dir, base, file)
	if err != nil || !ok {
		return false, err
	}
	after, ok, err := show(dir, head, file)
	if err != nil || !ok {
		return false, err
	}
	a, okA := normalize(before)
	b, okB := normalize(after)
	return okA && okB && bytes.Equal(a, b), nil
}

// show reads a file at a revision; ok is false when it does not exist there.
// The file is relative to dir, which may be a subdirectory of the repository.
func show(dir, rev, file string) ([]byte, bool, error) {
	object := rev + ":./" + file
	if out, err := exec.Command("git", "-C", dir, "cat-file", "-e", object).CombinedOutput(); err != nil {
		if _, exit := err.(*exec.ExitError); exit {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("git cat-file %s:%s: %w: %s", rev, file, err, out)
	}
	out, err := exec.Command("git", "-C", dir, "show", object).Output()
	if err != nil {
		return nil, false, fmt.Errorf("git show %s:%s: %w", rev, file, err)
	}
	return out, true, nil
}

// goCode prints the file as the compiler sees it: comments dropped, layout
// canonical. Struct tags and string contents are kept.
func goCode(src []byte) ([]byte, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	var out bytes.Buffer
	if err := (&printer.Config{Mode: printer.RawFormat}).Fprint(&out, fset, file); err != nil {
		return nil, false
	}
	return bytes.Join(bytes.Fields(out.Bytes()), []byte(" ")), true
}

// generatedTSCode removes comments and layout from protobuf-es output, keeping
// string and template literals verbatim and one space only where two words
// would otherwise join.
func generatedTSCode(src []byte) ([]byte, bool) {
	var out []byte
	space := false
	word := func(c byte) bool {
		return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	emit := func(c byte) {
		if space && len(out) > 0 && word(out[len(out)-1]) && word(c) {
			out = append(out, ' ')
		}
		space = false
		out = append(out, c)
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			space = true
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return nil, false
			}
			i += end + 3
			space = true
		case c == '"' || c == '\'' || c == '`':
			emit(c)
			for i++; ; i++ {
				if i >= len(src) {
					return nil, false
				}
				out = append(out, src[i])
				if src[i] == '\\' && i+1 < len(src) {
					i++
					out = append(out, src[i])
					continue
				}
				if src[i] == c {
					break
				}
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			space = true
		default:
			emit(c)
		}
	}
	return out, true
}
