package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func position(t *testing.T, kind FileKind, name string) FilePosition {
	t.Helper()
	p, err := NewFilePosition("owner-1", kind, name)
	if err != nil {
		t.Fatalf("NewFilePosition(%q): %v", name, err)
	}
	return p
}

// A name is one directory entry, so a position never leaves its space's
// directory of its kind.
func TestFileNameIsOneDirectoryEntry(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "bad\x00", "tab\tname", " lead", "trail "} {
		if _, err := NewFilePosition("owner-1", FileKindAttachment, name); !errors.Is(err, ErrInvalidFileName) {
			t.Fatalf("name %q: err = %v, want ErrInvalidFileName", name, err)
		}
	}
	if _, err := NewFilePosition("owner-1", "folder", "report.md"); !errors.Is(err, ErrInvalidFileKind) {
		t.Fatalf("kind err = %v", err)
	}
	if _, err := NewFilePosition("owner-1", FileKindArtifact, ".env.example"); err != nil {
		t.Fatalf("a hidden-looking name is still one entry: %v", err)
	}
}

// Only the space's own token admits a write.
func TestAdmitFileWriteOnlyOnTheSpaceToken(t *testing.T) {
	space, err := OpenFileSpace("owner-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(space.SpaceToken) < 40 {
		t.Fatalf("token %q is guessable", space.SpaceToken)
	}
	person, _ := PersonWriter("user-1")
	if err := space.AdmitFileWrite(space.SpaceToken, person); err != nil {
		t.Fatalf("own token refused: %v", err)
	}
	for _, token := range []string{"", "other", space.SpaceToken + "x"} {
		if err := space.AdmitFileWrite(token, person); !errors.Is(err, ErrSpaceTokenRejected) {
			t.Fatalf("token %q: err = %v", token, err)
		}
	}
	if err := space.AdmitFileWrite(space.SpaceToken, FileWriter{}); !errors.Is(err, ErrFileWriterRequired) {
		t.Fatalf("unnamed writer: %v", err)
	}
	other, _ := OpenFileSpace("owner-2")
	if other.SpaceToken == space.SpaceToken {
		t.Fatal("two spaces share a token")
	}
}

// A Share Link follows its file's rename and ends with its deletion; a
// revoked link stays revoked.
func TestShareLinksFollowRenameAndEndWithDeletion(t *testing.T) {
	space, _ := OpenFileSpace("owner-1")
	report := position(t, FileKindArtifact, "report.md")
	link, err := space.CreateShareLink(report, "user-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	attachmentLink, _ := space.CreateShareLink(position(t, FileKindAttachment, "report.md"), "user-1", time.Now())
	renamed := position(t, FileKindArtifact, "final.md")
	space.RecordRename(report, renamed)
	if link.Target != renamed {
		t.Fatalf("link target = %+v, want the new position", link.Target)
	}
	if attachmentLink.Target.Kind != FileKindAttachment || attachmentLink.Target.Name != "report.md" {
		t.Fatal("a rename moved a link of the other kind")
	}
	if got := space.ActiveShareLinks(renamed); len(got) != 1 {
		t.Fatalf("active links at new position = %d", len(got))
	}
	space.RecordDeletion(renamed)
	if link.Active() {
		t.Fatal("deleting the file left its link active")
	}
	space.RecordRename(renamed, report)
	if link.Target != renamed {
		t.Fatal("a revoked link was retargeted")
	}
	// A member ends only their own links; the rest stay and are counted.
	attachment := position(t, FileKindAttachment, "report.md")
	other, _ := space.CreateShareLink(attachment, "user-2", time.Now())
	if revoked, remaining := space.RevokeShareLinks(attachment, "user-1"); revoked != 1 || remaining != 1 || attachmentLink.Active() || !other.Active() {
		t.Fatalf("own revoke: revoked %d remaining %d", revoked, remaining)
	}
	if revoked, remaining := space.RevokeShareLinks(attachment, ""); revoked != 1 || remaining != 0 || other.Active() {
		t.Fatalf("revoke all: revoked %d remaining %d", revoked, remaining)
	}
	// Links seen at a free name were left by an unrecorded deletion and are
	// revoked; one created once a file is there stays.
	old := position(t, FileKindAttachment, "old.md")
	stale, _ := space.CreateShareLink(old, "user-1", time.Now())
	seen := space.ShareLinkTokens(old)
	fresh, _ := space.CreateShareLink(old, "user-2", time.Now())
	space.RevokeStaleShareLinks(old, seen)
	if stale.Active() || !fresh.Active() {
		t.Fatalf("stale active = %v, fresh active = %v", stale.Active(), fresh.Active())
	}
	if _, err := space.CreateShareLink(report, " ", time.Now()); !errors.Is(err, ErrSharerRequired) {
		t.Fatalf("anonymous share: %v", err)
	}
}

// The input source names the space without exposing its layout.
func TestInputSourceNamesTheSpace(t *testing.T) {
	space, _ := OpenFileSpace("owner-1")
	owner, err := OwnerOfInputSource(space.InputSource())
	if err != nil || owner != "owner-1" {
		t.Fatalf("owner = %q, %v", owner, err)
	}
	for _, source := range []string{"", "assets/inputs/owner-1", "file-space/a/b", "file-space/", "file-space/.x", "case-space/owner-1"} {
		if _, err := OwnerOfInputSource(source); err == nil {
			t.Fatalf("source %q accepted", source)
		}
	}
}

// An owner is opaque but one visible path element, so its space is one
// directory of its own.
func TestOwnerIsOneVisibleElement(t *testing.T) {
	for _, owner := range []string{"0195f0a3-7c1e-7d2a-9b11-2f3c4d5e6f70", "case:42", "Work_1"} {
		if _, err := OpenFileSpace(owner); err != nil {
			t.Fatalf("owner %q: %v", owner, err)
		}
	}
	if _, err := OpenFileSpace(" "); !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("empty owner: %v", err)
	}
	for _, owner := range []string{".", "..", ".hidden", "a/b", `a\b`, "bad\x00"} {
		if _, err := OpenFileSpace(owner); !errors.Is(err, ErrInvalidOwner) {
			t.Fatalf("owner %q: %v", owner, err)
		}
	}
}

// A HOME is reached by its owner, as its caller named it; the key of a
// person's or a Project purpose's HOME is the digest of that name, so HOMEs
// named before keep their keys.
func TestHomeOwnership(t *testing.T) {
	person, err := ParseHomeOwner("374124958416109673")
	if err != nil {
		t.Fatal(err)
	}
	coordinator, _ := ParseHomeOwner("project/p-1/pm")
	other, _ := ParseHomeOwner("project/p-1/reviewer")
	again, _ := ParseHomeOwner(" 374124958416109673 ")
	if person.Key() != again.Key() || person.Key() == coordinator.Key() || coordinator.Key() == other.Key() {
		t.Fatal("HOME keys do not follow ownership")
	}
	if digest := sha256.Sum256([]byte("project/p-1/pm")); coordinator.Key() != hex.EncodeToString(digest[:]) {
		t.Fatal("a HOME named before changed its key")
	}
	for _, owner := range []string{"", " ", "a\x00b"} {
		if _, err := ParseHomeOwner(owner); !errors.Is(err, ErrInvalidHomeOwner) {
			t.Fatalf("owner %q: %v", owner, err)
		}
	}
}

// A writer is exactly one person, or one Work.
func TestFileWriterNamesExactlyOneWriter(t *testing.T) {
	for name, make := range map[string]func() (FileWriter, error){
		"no person": func() (FileWriter, error) { return PersonWriter(" ") },
		"no Work":   func() (FileWriter, error) { return WorkWriter(" ") },
	} {
		if _, err := make(); !errors.Is(err, ErrFileWriterRequired) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	work, err := WorkWriter(" work-1 ")
	if err != nil || work.WorkID() != "work-1" || work.UserID() != "" || !work.named() {
		t.Fatalf("work writer = %+v, %v", work, err)
	}
}
