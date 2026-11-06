package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// contentFiles keeps what each write committed.
type contentFiles struct {
	writableFiles
	written map[domain.FilePosition][]byte
}

func (f contentFiles) Write(ctx context.Context, owner string, position domain.FilePosition, content io.Reader, attributes FileAttributes, replace bool) (StoredFile, error) {
	data, err := io.ReadAll(content)
	if err != nil {
		return StoredFile{}, err
	}
	if replace && !f.present[position] {
		return StoredFile{}, ErrFileNotFound
	}
	if !replace && f.present[position] {
		return StoredFile{}, ErrNameTaken
	}
	f.written[position] = data
	return f.writableFiles.Write(ctx, owner, position, bytes.NewReader(data), attributes, replace)
}

// noWorkspace fails the test when a publication reads a Session Workspace.
type noWorkspace struct {
	WorkingFilesPort
	t *testing.T
}

func (w noWorkspace) ReadPublicationSource(context.Context, string, domain.WorkspacePath, func(io.Reader) error) error {
	w.t.Fatal("content was published from a workspace")
	return nil
}

// A Work without a Session Workspace publishes the content it sent: the
// Artifact is that content, Create and Replace keep their semantics, and each
// commit is published as a file change of that Work.
func TestAWorkPublishesTheContentItSent(t *testing.T) {
	space, err := domain.OpenFileSpace("owner-1")
	if err != nil {
		t.Fatal(err)
	}
	files := contentFiles{writableFiles{&fakeFiles{present: map[domain.FilePosition]bool{}}}, map[domain.FilePosition][]byte{}}
	changes := &recordedChanges{}
	app := NewApplication(&fakeSpaces{spaces: map[string]*domain.FileSpace{"owner-1": space}}, Ports{Persistent: files, Working: noWorkspace{t: t}, Links: fakeLinks{}, FileChanges: changes}, LinkLifetimes{}, PreviewSettings{}, WorkspaceSettings{}, nil)
	ctx := t.Context()
	report, _ := domain.NewFilePosition("owner-1", domain.FileKindArtifact, "report.md")
	source := ArtifactSource{SpaceToken: space.SpaceToken, WorkID: "work-1", Content: []byte("draft")}

	created, err := app.PublishArtifact(ctx, source, "report.md", false)
	if err != nil || created.Attributes.WorkID != "work-1" || string(files.written[report]) != "draft" {
		t.Fatalf("create: %+v, %q, %v", created, files.written[report], err)
	}
	if _, err := app.PublishArtifact(ctx, source, "report.md", false); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("a create meets a taken name: %v", err)
	}
	source.Content = []byte{}
	if _, err := app.PublishArtifact(ctx, source, "report.md", true); err != nil || len(files.written[report]) != 0 {
		t.Fatalf("replace with an empty file: %q, %v", files.written[report], err)
	}
	want := []FileChange{
		{Owner: "owner-1", Kind: domain.FileKindArtifact, Name: "report.md", Operation: FileCreated, Writer: work("work-1")},
		{Owner: "owner-1", Kind: domain.FileKindArtifact, Name: "report.md", Operation: FileReplaced, Writer: work("work-1")},
	}
	if !reflect.DeepEqual(changes.changes, want) {
		t.Fatalf("published changes\n got %+v\nwant %+v", changes.changes, want)
	}
}

// A publication names exactly one source, a Session Workspace file needs the
// Work's workspace, sent content comes only from a Work without one, and it is
// bounded.
func TestAPublicationNamesOneBoundedSource(t *testing.T) {
	app, _, _, space := newTestApplication(t)
	for name, test := range map[string]struct {
		source ArtifactSource
		want   error
	}{
		"both a path and content":      {ArtifactSource{WorkspaceReference: "ws-1", Path: "out.md", Content: []byte("x")}, ErrInvalidRequest},
		"a path without a workspace":   {ArtifactSource{Path: "out.md"}, ErrInvalidRequest},
		"content from a workspace":     {ArtifactSource{WorkspaceReference: "ws-1", Content: []byte("x")}, domain.ErrSentContentWithWorkspace},
		"content above the bound":      {ArtifactSource{Content: make([]byte, domain.MaxSentContentBytes+1)}, ErrInvalidSource},
		"neither a path nor content":   {ArtifactSource{WorkspaceReference: "ws-1"}, ErrInvalidSource},
		"a path leaving the workspace": {ArtifactSource{WorkspaceReference: "ws-1", Path: "../out.md"}, ErrInvalidSource},
	} {
		test.source.SpaceToken, test.source.WorkID = space.SpaceToken, "work-1"
		if _, err := app.PublishArtifact(t.Context(), test.source, "out.md", false); !errors.Is(err, test.want) {
			t.Fatalf("%s: %v, want %v", name, err, test.want)
		}
	}
}
