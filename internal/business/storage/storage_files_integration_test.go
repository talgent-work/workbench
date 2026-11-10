//go:build integration

package storage

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	storageintegrationv1 "github.com/talgent-work/contracts/gen/go/storage/integration/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/pkg/storagepath"
)

func TestADirectoryOpensOnceAndAdmitsWritesOnlyOnItsToken(t *testing.T) {
	f := newFixture(t)
	first := f.open(fileSpace(caseInputs))
	require.Equal(t, first, f.open(fileSpace(caseInputs)), "opening is idempotent")
	require.DirExists(t, f.folder(fileSpace(caseInputs)))

	_, err := f.app.IssueUpload(t.Context(), application.IssueUpload{Position: at(caseInputs, "a.md"), SpaceToken: "forged", UploaderUserID: "user-1"})
	require.ErrorIs(t, err, domain.ErrSpaceTokenRejected)
	other := f.open(fileSpace(named("inputs", "subject", "case:2")))
	require.ErrorIs(t, f.app.DeleteFile(t.Context(), at(caseInputs, "a.md"), other, "user-1"), domain.ErrSpaceTokenRejected)
	files, err := f.app.ListFiles(t.Context(), named("inputs", "subject", "never-opened"))
	require.NoError(t, err)
	require.Empty(t, files, "a Directory never opened lists nothing")
	_, err = f.app.IssueUpload(t.Context(), application.IssueUpload{Position: at(named("inputs", "subject", "never-opened"), "a.md"), SpaceToken: first, UploaderUserID: "user-1"})
	require.ErrorIs(t, err, domain.ErrDirectoryNotOpen)
}

func TestWritesStateWhetherTheyReplaceAndShareLinksFollowThem(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	require.Equal(t, http.StatusOK, f.upload(caseInputs, token, "brief.md", "v1", false).StatusCode)
	brief := f.files(caseInputs)["brief.md"]
	require.Equal(t, "user-1", brief.Attributes.UploaderUserID)
	require.Equal(t, "text/markdown", brief.ContentType())

	// Create never replaces: a name taken after the link was issued is
	// refused at commit.
	stale, err := f.app.IssueUpload(t.Context(), application.IssueUpload{Position: at(caseInputs, "late.md"), SpaceToken: token, UploaderUserID: "user-1"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.upload(caseInputs, token, "late.md", "first", false).StatusCode)
	request, _ := http.NewRequest(http.MethodPut, stale.URL, strings.NewReader("second"))
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusConflict, response.StatusCode)

	// Replace keeps the position and its Share Link; the link shows the new content.
	share, err := f.app.CreateShareLink(t.Context(), at(caseInputs, "brief.md"), "user-2")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.upload(caseInputs, token, "brief.md", "v2", true).StatusCode)
	status, body, header := f.get(share.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "v2", body)
	require.Contains(t, header.Get("Content-Disposition"), `filename=brief.md`)
	require.Len(t, f.files(caseInputs)["brief.md"].ShareLinks, 1)

	// Rename moves the link; a taken target is refused.
	_, err = f.app.RenameFile(t.Context(), at(caseInputs, "brief.md"), token, "late.md", "user-1")
	require.ErrorIs(t, err, application.ErrNameTaken)
	renamed, err := f.app.RenameFile(t.Context(), at(caseInputs, "brief.md"), token, "final brief.md", "user-1")
	require.NoError(t, err)
	require.Len(t, renamed.ShareLinks, 1)
	status, body, _ = f.get(share.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "v2", body)

	// A file gone without Storage says so through its link; Delete ends it.
	require.NoError(t, os.Remove(filepath.Join(f.folder(fileSpace(caseInputs)), "late.md")))
	lateShare, err := f.app.CreateShareLink(t.Context(), at(caseInputs, "final brief.md"), "user-1")
	require.NoError(t, err)
	require.NoError(t, f.app.DeleteFile(t.Context(), at(caseInputs, "final brief.md"), token, "user-1"))
	require.NoError(t, f.app.DeleteFile(t.Context(), at(caseInputs, "final brief.md"), token, "user-1"), "deleting an absent file succeeds")
	for _, link := range []string{share.URL, lateShare.URL} {
		status, _, _ = f.get(link)
		require.Equal(t, http.StatusGone, status)
	}
	require.Empty(t, f.files(caseInputs))
}

// Content is staged outside the Directory until it is complete.
func TestUploadIsInvisibleUntilComplete(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	link, err := f.app.IssueUpload(t.Context(), application.IssueUpload{Position: at(caseInputs, "big.bin"), SpaceToken: token, UploaderUserID: "user-1"})
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
	pending := filepath.Join(f.persistent, juiceFSPrivate, "pending")
	require.Eventually(t, func() bool {
		entries, _ := os.ReadDir(pending)
		return len(entries) == 1
	}, 5*time.Second, 10*time.Millisecond)
	require.Empty(t, f.files(caseInputs), "a partial upload is not listed")
	require.NoError(t, writer.Close())
	response := <-done
	require.Equal(t, http.StatusOK, response.StatusCode)
	response.Body.Close()
	require.Equal(t, int64(4096), f.files(caseInputs)["big.bin"].SizeBytes)
}

// An upload over the limit is refused, whether it declares its length or
// streams past it, and leaves nothing behind.
func TestUploadIsBounded(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	oversized := strings.Repeat("x", fixtureMaxUploadBytes+1)
	response := f.upload(caseInputs, token, "declared.bin", oversized, false)
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	link, err := f.app.IssueUpload(t.Context(), application.IssueUpload{Position: at(caseInputs, "streamed.bin"), SpaceToken: token, UploaderUserID: "user-1"})
	require.NoError(t, err)
	request, _ := http.NewRequest(http.MethodPut, link.URL, io.MultiReader(strings.NewReader(oversized)))
	request.ContentLength = -1
	response, err = http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	response.Body.Close()
	require.Empty(t, f.files(caseInputs))
	entries, _ := os.ReadDir(filepath.Join(f.persistent, juiceFSPrivate, "pending"))
	require.Empty(t, entries, "a refused upload leaves no staged content")
}

func TestAccessLinksReadOneFile(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	f.upload(caseInputs, token, "notes.md", "hello", false)
	link, err := f.app.IssueAccessLink(t.Context(), at(caseInputs, "notes.md"), true)
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
	_, err = f.app.IssueAccessLink(t.Context(), at(caseInputs, "absent.md"), true)
	require.ErrorIs(t, err, application.ErrFileNotFound)

	// A file a Harness wrote into a Directory is no Attachment or Artifact.
	require.NoError(t, os.WriteFile(filepath.Join(f.folder(fileSpace(caseInputs)), "harness.md"), []byte("h"), 0o644))
	_, err = f.app.IssueAccessLink(t.Context(), at(caseInputs, "harness.md"), true)
	require.ErrorIs(t, err, application.ErrFileNotFound)
	_, err = f.app.CreateShareLink(t.Context(), at(caseInputs, "harness.md"), "user-1")
	require.ErrorIs(t, err, application.ErrFileNotFound, "only Attachments and Artifacts are shared")
	require.NotContains(t, f.files(caseInputs), "harness.md", "only files Storage wrote are listed")
}

// Publication keeps no record of earlier attempts: a retried create meets the
// taken name, and a replacement writes again.
func TestPublicationIsExplicitAndMeetsRetriesByItsOwnSemantics(t *testing.T) {
	f := newFixture(t)
	artifacts := named("artifacts", "subject", "case:1")
	token := f.open(fileSpace(artifacts))
	f.workspace("work-1", nil)
	f.workspaceFile("work-1", "dist/report.md", "draft")
	source := application.ArtifactSource{SpaceToken: token, WorkID: "work-1", WorkDirectory: "workspace", Path: "dist/report.md"}

	created, err := f.app.PublishArtifact(t.Context(), source, "report.md", false)
	require.NoError(t, err)
	require.Equal(t, "work-1", created.Attributes.WorkID)
	_, err = f.app.PublishArtifact(t.Context(), source, "report.md", false)
	require.ErrorIs(t, err, application.ErrNameTaken, "a retried create meets the name it took")

	share, err := f.app.CreateShareLink(t.Context(), at(artifacts, "report.md"), "user-1")
	require.NoError(t, err)
	f.workspaceFile("work-1", "dist/report.md", "final")
	replaced, err := f.app.PublishArtifact(t.Context(), source, "report.md", true)
	require.NoError(t, err)
	require.Len(t, replaced.ShareLinks, 1)
	_, body, _ := f.get(share.URL)
	require.Equal(t, "final", body)
	_, err = f.app.PublishArtifact(t.Context(), source, "absent.md", true)
	require.ErrorIs(t, err, application.ErrFileNotFound)

	require.NoError(t, f.app.DeleteArtifact(t.Context(), token, "report.md", "work-1"))
	require.NoError(t, f.app.DeleteArtifact(t.Context(), token, "report.md", "work-1"), "a retried delete finds nothing left")
	require.NotContains(t, f.files(artifacts), "report.md")
	status, _, _ := f.get(share.URL)
	require.Equal(t, http.StatusGone, status)
	_, err = f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: "forged", WorkID: "work-1", WorkDirectory: "workspace", Path: "dist/report.md"}, "x.md", false)
	require.ErrorIs(t, err, domain.ErrSpaceTokenRejected, "the Space Token alone names the File Space")
}

// Only one regular file of the Work Directory outside every Directory
// mounted in it is published, and no symbolic link is followed out of it.
func TestPublicationRefusesWhatIsNotOneWorkspaceFile(t *testing.T) {
	f := newFixture(t)
	artifacts := named("artifacts", "subject", "case:1")
	token := f.open(fileSpace(artifacts))
	f.workspace("work-1", map[string]application.Located{"inputs": fileSpace(caseInputs)})
	f.workspaceFile("work-1", "site/index.html", "<html>")
	workspace := f.folder(workDirectory("work-1", "workspace"))
	secret := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(secret, []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(workspace, "leak.md")))
	require.NoError(t, os.Symlink(filepath.Dir(secret), filepath.Join(workspace, "linked")))

	for _, path := range []string{"../other", "site", "leak.md", "linked/secret", "inputs/brief.md", "missing.md"} {
		source := application.ArtifactSource{SpaceToken: token, WorkID: "work-1", WorkDirectory: "workspace", Path: path}
		_, err := f.app.PublishArtifact(t.Context(), source, "out.md", false)
		// A Directory mounted in the Work Directory is an empty mount point
		// in Storage's space.
		if path == "missing.md" || path == "inputs/brief.md" {
			require.ErrorIs(t, err, application.ErrSourceNotFound, "path %q", path)
		} else {
			require.ErrorIs(t, err, application.ErrInvalidSource, "path %q", path)
		}
	}
	// A Directory of the Work that outlives it is no Work Directory.
	f.upload(named("inputs", "work", "work-1"), f.open(fileSpace(named("inputs", "work", "work-1"))), "brief.md", "b", false)
	_, err := f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: token, WorkID: "work-1", WorkDirectory: "inputs", Path: "brief.md"}, "out.md", false)
	require.ErrorIs(t, err, application.ErrInvalidSource, "a permanent Directory is no publication source")
	require.Empty(t, f.files(artifacts))
}

// The Directory file query lists the files Storage wrote into exactly the
// Directories named; one never opened has none, and one reclaimed answers
// that it was reclaimed.
func TestDirectoryFileQueryListsTheNamedDirectories(t *testing.T) {
	f := newFixture(t)
	first, second, other := named("artifacts", "work", "work-1"), named("artifacts", "work", "work-2"), named("artifacts", "work", "work-3")
	for _, directory := range []application.Named{first, second, other} {
		f.upload(directory, f.open(fileSpace(directory)), directory.Owner[0].Value+".md", "x", false)
	}
	scratch := workDirectory("work-1", "scratch")
	f.upload(scratch.Directory, f.open(scratch), "notes.md", "x", false)
	require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-1"))
	listed, err := f.app.ListDirectoryFiles(t.Context(), []application.Named{first, second, first, named("artifacts", "work", "never-opened"), scratch.Directory})
	require.NoError(t, err)
	var names []string
	for _, file := range listed.Files {
		names = append(names, file.Position.Name)
	}
	require.Equal(t, []string{"work-1.md", "work-2.md"}, names)
	require.Equal(t, []application.Named{scratch.Directory}, listed.Reclaimed, "unlike one never opened")
}

// A Share Link into a reclaimed Directory answers that it was reclaimed.
func TestAShareLinkIntoAReclaimedDirectoryAnswersReclaimed(t *testing.T) {
	f := newFixture(t)
	scratch := workDirectory("work-1", "scratch")
	f.upload(scratch.Directory, f.open(scratch), "notes.md", "x", false)
	share, err := f.app.CreateShareLink(t.Context(), at(scratch.Directory, "notes.md"), "user-1")
	require.NoError(t, err)
	require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-1"))
	_, err = f.app.ReadShared(t.Context(), share.Token)
	require.ErrorIs(t, err, domain.ErrDirectoryReclaimed)
	status, _, _ := f.get(share.URL)
	require.Equal(t, http.StatusGone, status)
}

// Files keep their origin on the file itself, laid out as the originals
// mount publishes them.
func TestFilesKeepTheirOriginOnTheFileItself(t *testing.T) {
	f := newFixture(t)
	f.upload(caseInputs, f.open(fileSpace(caseInputs)), "brief.md", "b", false)
	buf := make([]byte, 64)
	n, err := unix.Getxattr(filepath.Join(f.persistent, storagepath.OriginalsSubPath[len("talgent/"):], "subject=case:1", "inputs", "brief.md"), storagepath.AttributeUploader, buf)
	require.NoError(t, err)
	require.Equal(t, "user-1", string(buf[:n]))
}

// Each committed change Storage carries out is published with its Directory
// and writer; a deletion that finds nothing publishes nothing.
func TestCommittedFileChangesArePublished(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	f.upload(caseInputs, token, "a.md", "a", false)
	_, err := f.app.RenameFile(t.Context(), at(caseInputs, "a.md"), token, "b.md", "user-1")
	require.NoError(t, err)
	require.NoError(t, f.app.DeleteFile(t.Context(), at(caseInputs, "b.md"), token, "user-1"))
	require.NoError(t, f.app.DeleteFile(t.Context(), at(caseInputs, "b.md"), token, "user-1"))

	var changes []*storageintegrationv1.FileChangedV1
	require.Eventually(t, func() bool { return len(f.published.all()) == 3 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	for _, msg := range f.published.all() {
		require.Equal(t, `subject=case%3A1/inputs`, msg.Key())
		changes = append(changes, msg.Payload().(*storageintegrationv1.FileChangedV1))
	}
	require.Len(t, changes, 3, "nothing deleted, nothing published")
	operations := map[storageintegrationv1.FileOperation]*storageintegrationv1.FileChangedV1{}
	for _, change := range changes {
		operations[change.GetOperation()] = change
		require.Equal(t, "user-1", change.GetActorUserId())
		require.Equal(t, "inputs", change.GetDirectory().GetName())
		require.Equal(t, "subject", change.GetDirectory().GetOwner()[0].GetCondition())
		require.Equal(t, "case:1", change.GetDirectory().GetOwner()[0].GetValue())
	}
	require.Equal(t, "a.md", operations[storageintegrationv1.FileOperation_FILE_OPERATION_CREATED].GetName())
	require.Equal(t, "a.md", operations[storageintegrationv1.FileOperation_FILE_OPERATION_RENAMED].GetPreviousName())
	require.Equal(t, "b.md", operations[storageintegrationv1.FileOperation_FILE_OPERATION_DELETED].GetName())
}
