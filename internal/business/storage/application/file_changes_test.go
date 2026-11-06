package application

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func person(userID string) domain.FileWriter {
	writer, _ := domain.PersonWriter(userID)
	return writer
}

func work(workID string) domain.FileWriter {
	writer, _ := domain.WorkWriter(workID)
	return writer
}

// recordedChanges keeps every published file change.
type recordedChanges struct{ changes []FileChange }

func (r *recordedChanges) Publish(_ context.Context, change FileChange) {
	change.OccurredAt = time.Time{}
	r.changes = append(r.changes, change)
}

// writableFiles carries out writes, renames and removals on fakeFiles.
type writableFiles struct{ *fakeFiles }

func (f writableFiles) Write(_ context.Context, _ string, position domain.FilePosition, _ io.Reader, attributes FileAttributes, _ bool) (StoredFile, error) {
	f.present[position] = true
	return StoredFile{Position: position, Attributes: attributes}, nil
}

func (f writableFiles) Rename(_ context.Context, _ string, from, to domain.FilePosition) (StoredFile, error) {
	delete(f.present, from)
	f.present[to] = true
	return StoredFile{Position: to}, nil
}

func (f writableFiles) Remove(_ context.Context, _ string, position domain.FilePosition) (bool, error) {
	removed := f.present[position]
	delete(f.present, position)
	return removed, nil
}

type grantLinks struct {
	fakeLinks
	grant UploadGrant
}

func (l grantLinks) VerifyUpload(string, time.Time) (UploadGrant, error) { return l.grant, nil }

type workspaceSource struct{ WorkingFilesPort }

func (workspaceSource) ReadPublicationSource(_ context.Context, _ string, _ domain.WorkspacePath, read func(io.Reader) error) error {
	return read(strings.NewReader("report"))
}

// Every committed file change is published with who made it, a person or a
// Work; a deletion that finds nothing and a replayed publication publish
// nothing.
func TestCommittedFileChangesArePublished(t *testing.T) {
	space, err := domain.OpenFileSpace("owner-1")
	if err != nil {
		t.Fatal(err)
	}
	spaces := &fakeSpaces{spaces: map[string]*domain.FileSpace{"owner-1": space}}
	files := writableFiles{&fakeFiles{present: map[domain.FilePosition]bool{}}}
	links := &grantLinks{}
	changes := &recordedChanges{}
	app := NewApplication(spaces, Ports{Persistent: files, Working: workspaceSource{}, Links: links, FileChanges: changes}, LinkLifetimes{}, PreviewSettings{}, WorkspaceSettings{}, nil)
	ctx := t.Context()

	links.grant = UploadGrant{Owner: "owner-1", Name: "brief.md", UploaderUserID: "user-1"}
	if _, err := app.UploadAttachment(ctx, "grant", strings.NewReader("v1")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	links.grant.Replace = true
	if _, err := app.UploadAttachment(ctx, "grant", strings.NewReader("v2")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, err := app.RenameAttachment(ctx, "owner-1", space.SpaceToken, "brief.md", "final.md", "user-2"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := app.DeleteFile(ctx, "owner-1", space.SpaceToken, domain.FileKindAttachment, "final.md", "user-3"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := app.DeleteFile(ctx, "owner-1", space.SpaceToken, domain.FileKindAttachment, "final.md", "user-3"); err != nil {
		t.Fatalf("delete an absent file: %v", err)
	}
	source := ArtifactSource{SpaceToken: space.SpaceToken, WorkID: "work-1", WorkspaceReference: "ws-1", Path: "out/report.md"}
	if _, err := app.PublishArtifact(ctx, source, "report.md", false); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := app.DeleteArtifact(ctx, space.SpaceToken, "report.md", "work-1"); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}

	scope := func(change FileChange) FileChange {
		change.Owner = "owner-1"
		return change
	}
	want := []FileChange{
		scope(FileChange{Kind: domain.FileKindAttachment, Name: "brief.md", Operation: FileCreated, Writer: person("user-1")}),
		scope(FileChange{Kind: domain.FileKindAttachment, Name: "brief.md", Operation: FileReplaced, Writer: person("user-1")}),
		scope(FileChange{Kind: domain.FileKindAttachment, Name: "final.md", PreviousName: "brief.md", Operation: FileRenamed, Writer: person("user-2")}),
		scope(FileChange{Kind: domain.FileKindAttachment, Name: "final.md", Operation: FileDeleted, Writer: person("user-3")}),
		scope(FileChange{Kind: domain.FileKindArtifact, Name: "report.md", Operation: FileCreated, Writer: work("work-1")}),
		scope(FileChange{Kind: domain.FileKindArtifact, Name: "report.md", Operation: FileDeleted, Writer: work("work-1")}),
	}
	if !reflect.DeepEqual(changes.changes, want) {
		t.Fatalf("published changes\n got %+v\nwant %+v", changes.changes, want)
	}
}

// A change without its person or Work is refused before anything changes, so
// no publication lacks who made it.
func TestFileChangesNeedTheirActor(t *testing.T) {
	app, _, files, space := newTestApplication(t)
	brief, _ := domain.NewFilePosition("owner-1", domain.FileKindAttachment, "brief.md")
	files.present[brief] = true
	if _, err := app.RenameAttachment(t.Context(), "owner-1", space.SpaceToken, "brief.md", "final.md", " "); !errors.Is(err, domain.ErrFileWriterRequired) {
		t.Fatalf("rename without a person: %v", err)
	}
	if err := app.DeleteFile(t.Context(), "owner-1", space.SpaceToken, domain.FileKindAttachment, "brief.md", ""); !errors.Is(err, domain.ErrFileWriterRequired) {
		t.Fatalf("delete without a person: %v", err)
	}
	if err := app.DeleteArtifact(t.Context(), space.SpaceToken, "report.md", ""); !errors.Is(err, domain.ErrFileWriterRequired) {
		t.Fatalf("delete without a Work: %v", err)
	}
	if !files.present[brief] {
		t.Fatal("a refused change changed the file")
	}
}

// A removed file is published even when recording its deletion fails; the
// retry that records it removes nothing and publishes nothing again.
func TestAnInterruptedDeletionIsPublishedOnce(t *testing.T) {
	space, err := domain.OpenFileSpace("owner-1")
	if err != nil {
		t.Fatal(err)
	}
	spaces := &fakeSpaces{spaces: map[string]*domain.FileSpace{"owner-1": space}}
	files := writableFiles{&fakeFiles{present: map[domain.FilePosition]bool{}}}
	changes := &recordedChanges{}
	app := NewApplication(spaces, Ports{Persistent: files, Links: fakeLinks{}, FileChanges: changes}, LinkLifetimes{}, PreviewSettings{}, WorkspaceSettings{}, nil)
	brief, _ := domain.NewFilePosition("owner-1", domain.FileKindAttachment, "brief.md")
	files.present[brief] = true
	spaces.conflicts = spaceChangeAttempts
	if err := app.DeleteFile(t.Context(), "owner-1", space.SpaceToken, domain.FileKindAttachment, "brief.md", "user-1"); !errors.Is(err, domain.ErrConcurrentModification) {
		t.Fatalf("interrupted delete: %v", err)
	}
	if err := app.DeleteFile(t.Context(), "owner-1", space.SpaceToken, domain.FileKindAttachment, "brief.md", "user-1"); err != nil {
		t.Fatalf("retried delete: %v", err)
	}
	if len(changes.changes) != 1 || changes.changes[0].Operation != FileDeleted {
		t.Fatalf("published %+v, want the one deletion", changes.changes)
	}
}
