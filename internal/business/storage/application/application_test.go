package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// fakeSpaces keeps File Spaces in memory; conflicts makes the next saves
// lose to a concurrent change.
type fakeSpaces struct {
	spaces    map[string]*domain.FileSpace
	conflicts int
}

func (f *fakeSpaces) Get(_ context.Context, owner string) (*domain.FileSpace, error) {
	space, ok := f.spaces[owner]
	if !ok {
		return nil, domain.ErrSpaceNotOpen
	}
	copied := *space
	copied.ShareLinks = append([]*domain.ShareLink(nil), space.ShareLinks...)
	return &copied, nil
}

func (f *fakeSpaces) GetBySpaceToken(_ context.Context, token string) (*domain.FileSpace, error) {
	for owner, space := range f.spaces {
		if space.SpaceToken == token {
			return f.Get(context.Background(), owner)
		}
	}
	return nil, domain.ErrSpaceTokenRejected
}

func (f *fakeSpaces) GetByShareToken(context.Context, string) (*domain.FileSpace, error) {
	return nil, domain.ErrShareLinkNotFound
}

func (f *fakeSpaces) Save(_ context.Context, space *domain.FileSpace) error {
	if f.conflicts > 0 {
		f.conflicts--
		return domain.ErrConcurrentModification
	}
	f.spaces[space.Owner] = space
	return nil
}

// fakeFiles holds the names present at each position.
type fakeFiles struct {
	PersistentFilesPort
	present map[domain.FilePosition]bool
}

func (f *fakeFiles) Stat(_ context.Context, _ string, position domain.FilePosition) (StoredFile, error) {
	if !f.present[position] {
		return StoredFile{}, ErrFileNotFound
	}
	return StoredFile{Position: position}, nil
}

func (f *fakeFiles) List(_ context.Context, owner string) ([]StoredFile, error) {
	var files []StoredFile
	for position := range f.present {
		if position.Owner == owner {
			files = append(files, StoredFile{Position: position})
		}
	}
	return files, nil
}

type fakeLinks struct{ LinkPort }

func (fakeLinks) UploadURL(grant UploadGrant, _ time.Time) string { return "/upload/" + grant.Name }
func (fakeLinks) ShareURL(token string) string                    { return "/shares/" + token }

func newTestApplication(t *testing.T) (*Application, *fakeSpaces, *fakeFiles, *domain.FileSpace) {
	t.Helper()
	space, err := domain.OpenFileSpace("owner-1")
	if err != nil {
		t.Fatal(err)
	}
	spaces := &fakeSpaces{spaces: map[string]*domain.FileSpace{"owner-1": space}}
	files := &fakeFiles{present: map[domain.FilePosition]bool{}}
	return NewApplication(spaces, Ports{Persistent: files, Links: fakeLinks{}}, LinkLifetimes{}, PreviewSettings{}, WorkspaceSettings{}, nil), spaces, files, space
}

// A Create refuses a taken name and a Replace an absent one before any link
// is issued; the upload link lives the short upload lifetime.
func TestUploadLinksStateTheirIntent(t *testing.T) {
	app, _, files, space := newTestApplication(t)
	brief, _ := domain.NewFilePosition("owner-1", domain.FileKindAttachment, "brief.md")
	request := IssueAttachmentUpload{Owner: "owner-1", SpaceToken: space.SpaceToken, Name: "brief.md", UploaderUserID: "user-1"}
	link, err := app.IssueAttachmentUpload(t.Context(), request)
	if err != nil || link.URL != "/upload/brief.md" || time.Until(link.ExpiresAt) > defaultUploadTTL {
		t.Fatalf("create link %+v, %v", link, err)
	}
	request.Replace = true
	if _, err := app.IssueAttachmentUpload(t.Context(), request); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("replace of an absent file: %v", err)
	}
	files.present[brief] = true
	if _, err := app.IssueAttachmentUpload(t.Context(), request); err != nil {
		t.Fatalf("replace: %v", err)
	}
	request.Replace = false
	if _, err := app.IssueAttachmentUpload(t.Context(), request); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("create over a taken name: %v", err)
	}
	request.SpaceToken = "other"
	if _, err := app.IssueAttachmentUpload(t.Context(), request); !errors.Is(err, domain.ErrSpaceTokenRejected) {
		t.Fatalf("foreign token: %v", err)
	}
}

// A share retries a concurrent space change on fresh state, checks the file
// on every attempt, and gives up after a bounded number of attempts.
func TestShareLinkCreationRetriesBoundedly(t *testing.T) {
	app, spaces, files, _ := newTestApplication(t)
	brief, _ := domain.NewFilePosition("owner-1", domain.FileKindAttachment, "brief.md")
	files.present[brief] = true
	spaces.conflicts = spaceChangeAttempts - 1
	if _, err := app.CreateShareLink(t.Context(), "owner-1", domain.FileKindAttachment, "brief.md", "user-1"); err != nil {
		t.Fatalf("share after conflicts: %v", err)
	}
	spaces.conflicts = spaceChangeAttempts
	if _, err := app.CreateShareLink(t.Context(), "owner-1", domain.FileKindAttachment, "brief.md", "user-1"); !errors.Is(err, domain.ErrConcurrentModification) {
		t.Fatalf("unbounded retry: %v", err)
	}
	delete(files.present, brief)
	if _, err := app.CreateShareLink(t.Context(), "owner-1", domain.FileKindAttachment, "brief.md", "user-1"); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("share of a gone file: %v", err)
	}
}

// The Owner File Query lists each named owner's files once, an owner whose
// space never opened as none, and refuses an invalid owner or too many.
func TestOwnerFileQueryListsEachNamedOwnerOnce(t *testing.T) {
	app, _, files, _ := newTestApplication(t)
	brief, _ := domain.NewFilePosition("owner-1", domain.FileKindAttachment, "brief.md")
	other, _ := domain.NewFilePosition("owner-2", domain.FileKindArtifact, "report.md")
	files.present[brief], files.present[other] = true, true
	listed, err := app.ListOwnerFiles(t.Context(), []string{"owner-1", "owner-1", "never-opened"})
	if err != nil || len(listed) != 1 || listed[0].Position != brief {
		t.Fatalf("owner files %+v, %v", listed, err)
	}
	if _, err := app.ListOwnerFiles(t.Context(), []string{"owner-1", "../owner-2"}); !errors.Is(err, domain.ErrInvalidOwner) {
		t.Fatalf("invalid owner: %v", err)
	}
	if _, err := app.ListOwnerFiles(t.Context(), make([]string, maxOwnerFileQuery+1)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("too many owners: %v", err)
	}
}

func TestInputSourcesResolveToTheOwnersOfTheirSpaces(t *testing.T) {
	app, _, _, _ := newTestApplication(t)
	space := domain.FileSpace{Owner: "owner-1"}
	owners, err := app.ResolveInputSources([]string{space.InputSource(), "file-space/../escape", "elsewhere"})
	if err != nil || len(owners) != 3 || owners[0] != "owner-1" || owners[1] != "" || owners[2] != "" {
		t.Fatalf("owners %q, %v", owners, err)
	}
	if _, err := app.ResolveInputSources(make([]string, maxOwnerFileQuery+1)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("too many input sources: %v", err)
	}
}
