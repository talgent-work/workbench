//go:build integration

package storage

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/business/storage/infrastructure"
	storagehttp "github.com/talgent-work/workbench/internal/business/storage/transport/http"
	"github.com/talgent-work/workbench/internal/pkg/storagepath"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// fixture is Storage over two real directories standing for JuiceFS and the
// NAS, with File Spaces in a real MySQL schema and links served over HTTP.
type fixture struct {
	t          *testing.T
	app        *application.Application
	server     *httptest.Server
	persistent string
	working    string
	// nas is the working storage a sandbox writes its workspace through.
	nas *infrastructure.WorkingFiles
	// spaces can be made to fail every space change, as an interruption
	// between a file operation and its record would.
	spaces *interruptibleSpaces
	// converter stands for Gotenberg; queue for the rendering lane.
	converter *fakeConverter
	queue     *fakeRenderingQueue
}

// fixtureMaxUploadBytes bounds one upload in these tests.
const fixtureMaxUploadBytes = 16 << 10

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, func(*Config) {})
}

// newFixtureWith is a fixture whose configuration configure adjusts.
func newFixtureWith(t *testing.T, configure func(*Config)) *fixture {
	t.Helper()
	dsn := mysqltest.Database(t)
	engine, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	f := &fixture{t: t, persistent: t.TempDir(), working: t.TempDir()}
	f.nas = infrastructure.NewWorkingFiles(f.working, -1)
	mux := http.NewServeMux()
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	f.spaces = &interruptibleSpaces{FileSpaceRepository: infrastructure.NewFileSpaceRepository(engine)}
	f.converter, f.queue = newFakeConverter(t), newFakeRenderingQueue()
	config := Config{
		PersistentRoot: f.persistent, WorkingRoot: f.working, SandboxUID: -1,
		PublicBaseURL: f.server.URL, LinkSecret: "link-secret",
		AgentToken: "a", FileSpaceTokens: []string{"i"}, ReferenceSpaceTokens: []string{"k"},
		Mounts:  MountConfig{WorkingClaim: "nas-root", InputsClaim: "juicefs-inputs", InputsRoot: "talgent"},
		Preview: PreviewConfig{GotenbergAddress: f.converter.server.URL},
	}
	configure(&config)
	f.app, err = assemble(config, f.spaces, Rendering{Enqueuer: f.queue, Tasks: f.queue}, nil, slog.Default())
	require.NoError(t, err)
	storagehttp.Register(mux, f.app, fixtureMaxUploadBytes)
	return f
}

func (f *fixture) open(owner string) application.OpenedSpace {
	opened, err := f.app.OpenFileSpace(f.t.Context(), owner)
	require.NoError(f.t, err)
	return opened
}

// upload writes an Attachment through its upload link, as a browser would.
func (f *fixture) upload(owner, token, name, content string, replace bool) *http.Response {
	return f.uploadAs(owner, token, name, "text/markdown", content, replace)
}

// uploadAs uploads with the declared contentType, empty when none is known.
func (f *fixture) uploadAs(owner, token, name, contentType, content string, replace bool) *http.Response {
	link, err := f.app.IssueAttachmentUpload(f.t.Context(), application.IssueAttachmentUpload{Owner: owner, SpaceToken: token, Name: name, Replace: replace, UploaderUserID: "user-1", ContentType: contentType})
	require.NoError(f.t, err)
	request, _ := http.NewRequest(http.MethodPut, link.URL, strings.NewReader(content))
	response, err := http.DefaultClient.Do(request)
	require.NoError(f.t, err)
	f.t.Cleanup(func() { response.Body.Close() })
	return response
}

func (f *fixture) get(rawURL string) (int, string, http.Header) {
	response, err := http.Get(rawURL)
	require.NoError(f.t, err)
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body), response.Header
}

func (f *fixture) files(owner string) map[string]application.SpaceFile {
	files, err := f.app.ListFiles(f.t.Context(), owner)
	require.NoError(f.t, err)
	byName := map[string]application.SpaceFile{}
	for _, file := range files {
		byName[string(file.Position.Kind)+"/"+file.Position.Name] = file
	}
	return byName
}

// workspaceFile writes a file into a Work's workspace as its sandbox would.
func (f *fixture) workspaceFile(reference, relative, content string) {
	directory, err := f.nas.PrepareWorkspace(f.t.Context(), reference, nil)
	require.NoError(f.t, err)
	target := filepath.Join(f.working, directory, relative)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(f.t, os.WriteFile(target, []byte(content), 0o644))
}

func TestFileSpaceOpensOnceAndAdmitsOnlyItsToken(t *testing.T) {
	f := newFixture(t)
	first := f.open("case-1")
	again := f.open("case-1")
	require.Equal(t, first, again, "opening is idempotent")
	require.DirExists(t, filepath.Join(f.persistent, "assets/spaces", storagepath.Attachments("case-1")))

	_, err := f.app.IssueAttachmentUpload(t.Context(), application.IssueAttachmentUpload{Owner: "case-1", SpaceToken: "forged", Name: "a.md", UploaderUserID: "user-1"})
	require.ErrorIs(t, err, domain.ErrSpaceTokenRejected)
	other := f.open("case-2")
	require.ErrorIs(t, f.app.DeleteFile(t.Context(), "case-1", other.SpaceToken, domain.FileKindAttachment, "a.md", "user-1"), domain.ErrSpaceTokenRejected)
	files, err := f.app.ListFiles(t.Context(), "never-opened")
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestAttachmentWritesStateWhetherTheyReplace(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	require.Equal(t, http.StatusOK, f.upload("case-1", space.SpaceToken, "brief.md", "v1", false).StatusCode)
	brief := f.files("case-1")["attachment/brief.md"]
	require.Equal(t, "user-1", brief.Attributes.UploaderUserID)
	require.Equal(t, "text/markdown", brief.ContentType())

	// Create never replaces: a name taken after the link was issued is
	// refused at commit.
	stale, err := f.app.IssueAttachmentUpload(t.Context(), application.IssueAttachmentUpload{Owner: "case-1", SpaceToken: space.SpaceToken, Name: "late.md", UploaderUserID: "user-1"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.upload("case-1", space.SpaceToken, "late.md", "first", false).StatusCode)
	request, _ := http.NewRequest(http.MethodPut, stale.URL, strings.NewReader("second"))
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusConflict, response.StatusCode)

	// Replace keeps the position and its Share Link; the link shows the new content.
	share, err := f.app.CreateShareLink(t.Context(), "case-1", domain.FileKindAttachment, "brief.md", "user-2")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.upload("case-1", space.SpaceToken, "brief.md", "v2", true).StatusCode)
	status, body, header := f.get(share.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "v2", body)
	require.Contains(t, header.Get("Content-Disposition"), `filename=brief.md`)
	require.Len(t, f.files("case-1")["attachment/brief.md"].ShareLinks, 1)

	// Rename moves the link; a taken target is refused.
	_, err = f.app.RenameAttachment(t.Context(), "case-1", space.SpaceToken, "brief.md", "late.md", "user-1")
	require.ErrorIs(t, err, application.ErrNameTaken)
	renamed, err := f.app.RenameAttachment(t.Context(), "case-1", space.SpaceToken, "brief.md", "final brief.md", "user-1")
	require.NoError(t, err)
	require.Len(t, renamed.ShareLinks, 1)
	status, body, _ = f.get(share.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "v2", body)

	// Delete ends the link.
	require.NoError(t, f.app.DeleteFile(t.Context(), "case-1", space.SpaceToken, domain.FileKindAttachment, "final brief.md", "user-1"))
	require.NoError(t, f.app.DeleteFile(t.Context(), "case-1", space.SpaceToken, domain.FileKindAttachment, "final brief.md", "user-1"), "deleting an absent file succeeds")
	status, _, _ = f.get(share.URL)
	require.Equal(t, http.StatusGone, status)
	require.NotContains(t, f.files("case-1"), "attachment/final brief.md")
}

// Content is staged outside the space's directory until it is complete.
func TestUploadIsInvisibleUntilComplete(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	link, err := f.app.IssueAttachmentUpload(t.Context(), application.IssueAttachmentUpload{Owner: "case-1", SpaceToken: space.SpaceToken, Name: "big.bin", UploaderUserID: "user-1"})
	require.NoError(t, err)
	reader, writer := io.Pipe()
	done := make(chan *http.Response, 1)
	go func() {
		request, _ := http.NewRequest(http.MethodPut, link.URL, reader)
		response, _ := http.DefaultClient.Do(request)
		done <- response
	}()
	_, err = writer.Write(bytes.Repeat([]byte("x"), 4096))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		entries, _ := os.ReadDir(filepath.Join(f.persistent, "assets/pending"))
		return len(entries) == 1
	}, 5*time.Second, 10*time.Millisecond)
	require.Empty(t, f.files("case-1"), "a partial upload is not listed")
	require.NoDirExists(t, filepath.Join(f.persistent, "assets/spaces", storagepath.Attachments("case-1"), "big.bin"))
	require.NoError(t, writer.Close())
	response := <-done
	require.Equal(t, http.StatusOK, response.StatusCode)
	response.Body.Close()
	require.Equal(t, int64(4096), f.files("case-1")["attachment/big.bin"].SizeBytes)
}

// An upload over the limit is refused, whether it declares its length or
// streams past it, and leaves nothing behind.
func TestUploadIsBounded(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	oversized := strings.Repeat("x", fixtureMaxUploadBytes+1)
	response := f.upload("case-1", space.SpaceToken, "declared.bin", oversized, false)
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	response.Body.Close()
	link, err := f.app.IssueAttachmentUpload(t.Context(), application.IssueAttachmentUpload{Owner: "case-1", SpaceToken: space.SpaceToken, Name: "streamed.bin", UploaderUserID: "user-1"})
	require.NoError(t, err)
	request, _ := http.NewRequest(http.MethodPut, link.URL, io.MultiReader(strings.NewReader(oversized)))
	request.ContentLength = -1
	response, err = http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	response.Body.Close()
	require.Empty(t, f.files("case-1"))
	entries, _ := os.ReadDir(filepath.Join(f.persistent, "assets/pending"))
	require.Empty(t, entries, "a refused upload leaves no staged content")
}

func TestAccessLinksReadOneFile(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	f.upload("case-1", space.SpaceToken, "notes.md", "hello", false)
	link, err := f.app.IssueAccessLink(t.Context(), "case-1", domain.FileKindAttachment, "notes.md", true)
	require.NoError(t, err)
	status, body, header := f.get(link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "hello", body)
	require.True(t, strings.HasPrefix(header.Get("Content-Disposition"), "attachment"))

	forged, _ := url.Parse(link.URL)
	query := forged.Query()
	query.Set("grant", query.Get("grant")+"x")
	forged.RawQuery = query.Encode()
	status, _, _ = f.get(forged.String())
	require.Equal(t, http.StatusUnauthorized, status)
	_, err = f.app.IssueAccessLink(t.Context(), "case-1", domain.FileKindArtifact, "notes.md", true)
	require.ErrorIs(t, err, application.ErrFileNotFound)
}

// Publication keeps no record of earlier attempts: a retried create meets the
// taken name, and a replacement writes again.
func TestPublicationIsExplicitAndMeetsRetriesByItsOwnSemantics(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	f.workspaceFile("ws-1", "dist/report.md", "draft")
	source := application.ArtifactSource{SpaceToken: space.SpaceToken, WorkID: "work-1", WorkspaceReference: "ws-1", Path: "dist/report.md"}

	created, err := f.app.PublishArtifact(t.Context(), source, "report.md", false)
	require.NoError(t, err)
	require.Equal(t, "work-1", created.Attributes.WorkID)
	_, err = f.app.PublishArtifact(t.Context(), source, "report.md", false)
	require.ErrorIs(t, err, application.ErrNameTaken, "a retried create meets the name it took")

	share, err := f.app.CreateShareLink(t.Context(), "case-1", domain.FileKindArtifact, "report.md", "user-1")
	require.NoError(t, err)
	f.workspaceFile("ws-1", "dist/report.md", "final")
	replaced, err := f.app.PublishArtifact(t.Context(), source, "report.md", true)
	require.NoError(t, err)
	require.Len(t, replaced.ShareLinks, 1)
	_, body, _ := f.get(share.URL)
	require.Equal(t, "final", body)
	_, err = f.app.PublishArtifact(t.Context(), source, "absent.md", true)
	require.ErrorIs(t, err, application.ErrFileNotFound)

	require.NoError(t, f.app.DeleteArtifact(t.Context(), space.SpaceToken, "report.md", "work-1"))
	require.NoError(t, f.app.DeleteArtifact(t.Context(), space.SpaceToken, "report.md", "work-1"), "a retried delete finds nothing left")
	require.NotContains(t, f.files("case-1"), "artifact/report.md")
	status, _, _ := f.get(share.URL)
	require.Equal(t, http.StatusGone, status)
}

// Only one regular file of the workspace outside inputs/ is published, and no
// symbolic link is followed out of the workspace.
func TestPublicationRefusesWhatIsNotOneWorkspaceFile(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	f.workspaceFile("ws-1", "site/index.html", "<html>")
	f.workspaceFile("ws-1", "inputs-copy.md", "x")
	directory, err := f.nas.PrepareWorkspace(t.Context(), "ws-1", nil)
	require.NoError(t, err)
	workspace := filepath.Join(f.working, directory)
	secret := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(secret, []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(workspace, "leak.md")))
	require.NoError(t, os.Symlink(filepath.Dir(secret), filepath.Join(workspace, "linked")))

	// Path syntax is domain.TestWorkspacePaths'; one case keeps its mapping.
	for _, path := range []string{"../other", "site", "leak.md", "linked/secret", "missing.md"} {
		source := application.ArtifactSource{SpaceToken: space.SpaceToken, WorkID: "work-1", WorkspaceReference: "ws-1", Path: path}
		_, err := f.app.PublishArtifact(t.Context(), source, "out.md", false)
		require.Error(t, err, "path %q", path)
		if path == "missing.md" {
			require.ErrorIs(t, err, application.ErrSourceNotFound)
		} else {
			require.ErrorIs(t, err, application.ErrInvalidSource, "path %q", path)
		}
	}
	require.Empty(t, f.files("case-1"))
}

func TestMountsNameSpacesByBusinessIdentity(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	request := application.MountRequest{HomeOwner: "user-1", WorkID: "work-1", WorkspaceReference: "ws-1", InputSource: space.InputSource, SpaceToken: space.SpaceToken}
	mounts, err := f.app.AcquireMounts(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "nas-root", mounts.Workspace.ClaimName)
	require.False(t, mounts.Workspace.ReadOnly)
	require.False(t, mounts.Home.ReadOnly)
	require.NotNil(t, mounts.Inputs)
	require.True(t, mounts.Inputs.ReadOnly)
	require.Equal(t, "juicefs-inputs", mounts.Inputs.ClaimName)
	require.Equal(t, filepath.Join("talgent/assets/spaces", storagepath.Attachments("case-1")), mounts.Inputs.SubPath)
	require.DirExists(t, filepath.Join(f.working, mounts.Workspace.SubPath, "inputs"))

	// A restart reaches the same workspace and HOME, content retained.
	require.NoError(t, os.WriteFile(filepath.Join(f.working, mounts.Home.SubPath, "memory.md"), []byte("m"), 0o644))
	again, err := f.app.AcquireMounts(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, mounts, again)
	require.FileExists(t, filepath.Join(f.working, again.Home.SubPath, "memory.md"))
	elsewhere, err := f.app.AcquireMounts(t.Context(), application.MountRequest{HomeOwner: "user-1", WorkID: "work-2", WorkspaceReference: "ws-2"})
	require.NoError(t, err)
	require.Equal(t, mounts.Home, elsewhere.Home, "a person's Work Agent uses one HOME in every Project")
	require.NotEqual(t, mounts.Workspace, elsewhere.Workspace)
	require.Nil(t, elsewhere.Inputs)
	pm, err := f.app.AcquireMounts(t.Context(), application.MountRequest{HomeOwner: "project/project-1/pm", WorkID: "work-3", WorkspaceReference: "ws-3"})
	require.NoError(t, err)
	require.NotEqual(t, mounts.Home, pm.Home)

	request.SpaceToken = "forged"
	_, err = f.app.AcquireMounts(t.Context(), request)
	require.ErrorIs(t, err, domain.ErrSpaceTokenRejected)
}

func TestWorkspaceViewShowsAttachmentsUnderInputs(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	f.upload("case-1", space.SpaceToken, "brief.md", "b", false)
	f.workspaceFile("ws-1", "notes/todo.md", "t")
	root, err := f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1", InputSource: space.InputSource}, "")
	require.NoError(t, err)
	names := []string{}
	for _, entry := range root.Entries {
		names = append(names, entry.Name)
	}
	require.ElementsMatch(t, []string{"inputs", "notes"}, names)
	inputs, err := f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1", InputSource: space.InputSource}, "inputs")
	require.NoError(t, err)
	require.Len(t, inputs.Entries, 1)
	require.Equal(t, "brief.md", inputs.Entries[0].Name)
	require.Equal(t, &domain.FilePosition{Owner: "case-1", Kind: domain.FileKindAttachment, Name: "brief.md"}, inputs.Entries[0].Attachment, "inputs/ shows Attachments by their File position")
	_, err = f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1", InputSource: space.InputSource}, "../x")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath)
}

// Reclamation waits for a publication copying from the workspace and leaves
// every other space untouched.
func TestReclamationWaitsForCopiesAndTouchesNothingElse(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	f.upload("case-1", space.SpaceToken, "brief.md", "b", false)
	mounts, err := f.app.AcquireMounts(t.Context(), application.MountRequest{HomeOwner: "user-1", WorkID: "work-1", WorkspaceReference: "ws-1"})
	require.NoError(t, err)
	other, err := f.app.AcquireMounts(t.Context(), application.MountRequest{HomeOwner: "user-1", WorkID: "work-2", WorkspaceReference: "ws-2"})
	require.NoError(t, err)
	f.workspaceFile("ws-1", "out.md", "o")

	copying, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- f.nas.ReadPublicationSource(context.Background(), "ws-1", "out.md", func(io.Reader) error {
			close(copying)
			<-release
			return nil
		})
	}()
	<-copying
	require.NoError(t, f.app.RequestWorkspaceReclamation(t.Context(), "work-1", "ws-1"))
	require.NoError(t, f.app.ReclaimRequestedWorkspaces(t.Context()))
	require.DirExists(t, filepath.Join(f.working, mounts.Workspace.SubPath), "a copy in progress keeps its source")
	close(release)
	require.NoError(t, <-finished)
	require.NoError(t, f.app.ReclaimRequestedWorkspaces(t.Context()))
	require.NoDirExists(t, filepath.Join(f.working, mounts.Workspace.SubPath))
	require.DirExists(t, filepath.Join(f.working, mounts.Home.SubPath))
	require.DirExists(t, filepath.Join(f.working, other.Workspace.SubPath))
	require.Contains(t, f.files("case-1"), "attachment/brief.md")
	entries, err := os.ReadDir(filepath.Join(f.working, ".reclaim"))
	require.NoError(t, err)
	require.Len(t, entries, 2, "only the request and reclaimed records remain")

	// A publication after reclamation finds no source.
	_, err = f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: space.SpaceToken, WorkID: "work-1", WorkspaceReference: "ws-1", Path: "out.md"}, "out.md", false)
	require.ErrorIs(t, err, application.ErrSourceNotFound)
}

func TestFilesKeepTheirAttributesOnTheFileItself(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	f.upload("case-1", space.SpaceToken, "brief.md", "b", false)
	buf := make([]byte, 64)
	n, err := unix.Getxattr(filepath.Join(f.persistent, "assets/spaces", storagepath.Attachments("case-1"), "brief.md"), storagepath.AttributeUploader, buf)
	require.NoError(t, err)
	require.Equal(t, "user-1", string(buf[:n]))
}

// The Owner File Query lists the files of exactly the owners named, each
// with its owner; an owner whose space never opened has none.
func TestOwnerFileQueryListsTheNamedOwners(t *testing.T) {
	f := newFixture(t)
	first, second, other := f.open("work-1"), f.open("work-2"), f.open("work-3")
	f.upload("work-1", first.SpaceToken, "brief.md", "b", false)
	f.upload("work-2", second.SpaceToken, "notes.md", "n", false)
	f.upload("work-3", other.SpaceToken, "private.md", "p", false)
	f.workspaceFile("ws-2", "out/report.md", "r")
	_, err := f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: second.SpaceToken, WorkID: "work-2", WorkspaceReference: "ws-2", Path: "out/report.md"}, "report.md", false)
	require.NoError(t, err)

	files, err := f.app.ListOwnerFiles(t.Context(), []string{"work-1", "work-2", "work-1", "never-opened"})
	require.NoError(t, err)
	var listed []string
	for _, file := range files {
		listed = append(listed, file.Position.Owner+"/"+string(file.Position.Kind)+"/"+file.Position.Name)
	}
	require.Equal(t, []string{"work-1/attachment/brief.md", "work-2/attachment/notes.md", "work-2/artifact/report.md"}, listed)
}

// A Work Agent's Space Token alone names the File Space it publishes into.
func TestPublicationFindsItsSpaceByTheSpaceToken(t *testing.T) {
	f := newFixture(t)
	space := f.open("work-1")
	f.workspaceFile("ws-1", "out.md", "o")
	_, err := f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: "forged", WorkID: "work-1", WorkspaceReference: "ws-1", Path: "out.md"}, "out.md", false)
	require.ErrorIs(t, err, domain.ErrSpaceTokenRejected)
	published, err := f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: space.SpaceToken, WorkID: "work-1", WorkspaceReference: "ws-1", Path: "out.md"}, "out.md", false)
	require.NoError(t, err)
	require.Equal(t, "work-1", published.Position.Owner)
}
