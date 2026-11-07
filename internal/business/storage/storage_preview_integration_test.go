//go:build integration

package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	storagetaskv1 "github.com/talgent-work/workbench/gen/storage/task/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	applicationtask "github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// fakeConverter answers Gotenberg's LibreOffice route with the status the
// test sets, a PDF on success, and counts conversions.
type fakeConverter struct {
	server *httptest.Server
	mu     sync.Mutex
	status int
	body   string
	pdf    string
	calls  int
	names  []string
}

func newFakeConverter(t *testing.T) *fakeConverter {
	c := &fakeConverter{status: http.StatusOK, pdf: "%PDF-1.7 rendered"}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.calls++
		if file, header, err := r.FormFile("files"); err == nil {
			file.Close()
			c.names = append(c.names, header.Filename)
		}
		w.WriteHeader(c.status)
		if c.status == http.StatusOK {
			_, _ = io.WriteString(w, c.pdf)
			return
		}
		_, _ = io.WriteString(w, c.body)
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *fakeConverter) answer(status int) {
	c.answerWith(status, "upstream detail that never reaches a reader")
}

func (c *fakeConverter) answerWith(status int, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status, c.body = status, body
}

func (c *fakeConverter) conversions() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// fakeRenderingQueue keeps keyed tasks as the provider does: a task waiting
// or running for a key absorbs further enqueues of it.
type fakeRenderingQueue struct {
	mu    sync.Mutex
	tasks map[string]taskqueue.Task
}

func newFakeRenderingQueue() *fakeRenderingQueue {
	return &fakeRenderingQueue{tasks: map[string]taskqueue.Task{}}
}

func (q *fakeRenderingQueue) Enqueue(_ context.Context, task taskqueue.Task, _ ...taskqueue.EnqueueOption) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, waiting := q.tasks[task.ID()]; !waiting {
		q.tasks[task.ID()] = task
	}
	return nil
}

func (q *fakeRenderingQueue) Pending(queue, key string) (bool, error) {
	if queue != applicationtask.Queue {
		return false, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	_, waiting := q.tasks[key]
	return waiting, nil
}

// run works every queued task once, as the lane's worker would; a busy
// converter keeps its task for a later retry.
func (q *fakeRenderingQueue) run(t *testing.T, app *application.Application) {
	t.Helper()
	q.mu.Lock()
	tasks := make([]taskqueue.Task, 0, len(q.tasks))
	for _, task := range q.tasks {
		tasks = append(tasks, task)
	}
	q.mu.Unlock()
	for _, task := range tasks {
		payload := &storagetaskv1.RenderPreviewTaskV1{}
		require.NoError(t, proto.Unmarshal(task.Payload(), payload))
		err := app.RenderPreview(t.Context(), applicationtask.RenderPreview{
			Owner: payload.GetOwner(), Kind: payload.GetKind(), Name: payload.GetName(), Digest: payload.GetDigest(),
		})
		if errors.Is(err, application.ErrConverterBusy) {
			continue
		}
		require.NoError(t, err)
		q.mu.Lock()
		delete(q.tasks, task.ID())
		q.mu.Unlock()
	}
}

func (q *fakeRenderingQueue) size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.tasks)
}

func (f *fixture) requestPreview(ownerID, name string) application.FilePreview {
	f.t.Helper()
	preview, err := f.app.RequestFilePreview(f.t.Context(), ownerID, domain.FileKindAttachment, name)
	require.NoError(f.t, err)
	return preview
}

func previewEntries(t *testing.T, f *fixture) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.persistent, "assets/previews"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// Writing renders nothing; the first opening asks for one rendering however
// many readers ask, and the copy then shows at once, inline, while a
// download stays the original.
func TestPreviewCopyIsRenderedOnceOnOpening(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	docx := fixtureFile(t, "letter.docx")
	f.uploadAs("case-1", space.SpaceToken, "letter.docx", "", docx, false)
	require.Zero(t, f.converter.conversions(), "writing a file renders nothing")
	require.Empty(t, previewEntries(t, f))

	opened := f.preview("case-1", "letter.docx")
	require.Equal(t, domain.PreviewRender, opened.Verdict.Mode)
	require.False(t, opened.Rendering, "asking how a file shows renders nothing")
	for range 3 {
		requested := f.requestPreview("case-1", "letter.docx")
		require.True(t, requested.Rendering)
	}
	require.Equal(t, 1, f.queue.size(), "one content is one rendering")
	require.True(t, f.preview("case-1", "letter.docx").Rendering)

	f.queue.run(t, f.app)
	require.Equal(t, 1, f.converter.conversions())
	require.Equal(t, []string{"source.docx"}, f.converter.names, "no file name reaches the converter")
	shown := f.preview("case-1", "letter.docx")
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewShowCopy, Display: domain.PreviewDisplayPDF}, shown.Verdict)
	status, body, header := f.get(shown.Link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "%PDF-1.7 rendered", body)
	require.Equal(t, "application/pdf", header.Get("Content-Type"))
	require.True(t, strings.HasPrefix(header.Get("Content-Disposition"), "inline"))
	require.Equal(t, []string{f.files("case-1")["attachment/letter.docx"].Attributes.ContentDigest + ".pdf"}, previewEntries(t, f))
	_, err := os.Stat(filepath.Join(f.persistent, "assets/spaces/case-1/previews"))
	require.ErrorIs(t, err, os.ErrNotExist, "copies live outside the Project directories the originals mount shows")

	link, err := f.app.IssueAccessLink(t.Context(), "case-1", domain.FileKindAttachment, "letter.docx", true)
	require.NoError(t, err)
	_, body, _ = f.get(link.URL)
	require.Equal(t, docx, body, "a download is always the original")

	// The same content elsewhere shares the copy; Storage knows no Done, so
	// a finished owner's files show and render alike.
	other := f.open("case-2")
	f.uploadAs("case-2", other.SpaceToken, "copy.docx", "", docx, false)
	require.Equal(t, domain.PreviewShowCopy, f.preview("case-2", "copy.docx").Verdict.Mode)
	f.requestPreview("case-2", "copy.docx")
	require.Zero(t, f.queue.size())
	require.Equal(t, 1, f.converter.conversions())
}

// A replaced file never shows its old content's copy, and deleting, replacing
// or renaming leaves copies to lapse.
func TestReplacedFileShowsOnlyItsNewContent(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	f.uploadAs("case-1", space.SpaceToken, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.requestPreview("case-1", "letter.docx")
	f.queue.run(t, f.app)
	require.Len(t, previewEntries(t, f), 1)

	// Bytes after a ZIP's end record keep it valid: new content, same kind.
	f.uploadAs("case-1", space.SpaceToken, "letter.docx", "", fixtureFile(t, "letter.docx")+"\n", true)
	replaced := f.preview("case-1", "letter.docx")
	require.Equal(t, domain.PreviewRender, replaced.Verdict.Mode, "the old content's copy never shows")
	_, err := f.app.RenameAttachment(t.Context(), "case-1", space.SpaceToken, "letter.docx", "renamed.docx", "user-1")
	require.NoError(t, err)
	require.Len(t, previewEntries(t, f), 1, "a rename leaves the copy")
	require.NoError(t, f.app.DeleteFile(t.Context(), "case-1", space.SpaceToken, domain.FileKindAttachment, "renamed.docx", "user-1"))
	require.Len(t, previewEntries(t, f), 1, "a deletion leaves the copy to lapse")
}

// What the converter finds about the content is recorded and shown at the
// next opening without converting again; anything else leaves nothing.
func TestRenderingFailures(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	f.uploadAs("case-1", space.SpaceToken, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.uploadAs("case-1", space.SpaceToken, "letter.odt", "", fixtureFile(t, "letter.odt"), false)
	f.uploadAs("case-1", space.SpaceToken, "sheet.ods", "", fixtureFile(t, "sheet.ods"), false)

	f.converter.answerWith(http.StatusBadRequest, "The document 'source.docx' is password-protected. Provide its password in the 'password' form field.")
	f.requestPreview("case-1", "letter.docx")
	f.queue.run(t, f.app)
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewRefused, Refusal: domain.PreviewRefusalEncrypted}, f.preview("case-1", "letter.docx").Verdict)
	f.requestPreview("case-1", "letter.docx")
	require.Zero(t, f.queue.size(), "a recorded failure is not rendered again")

	// A temporary failure leaves nothing; which answers are temporary is
	// TestGotenbergAnswers' and TestSettleRendering's.
	f.converter.answer(http.StatusInternalServerError)
	require.True(t, f.requestPreview("case-1", "letter.odt").Rendering)
	f.queue.run(t, f.app)
	after := f.preview("case-1", "letter.odt")
	require.Equal(t, domain.PreviewRender, after.Verdict.Mode, "a temporary failure leaves nothing")
	require.False(t, after.Rendering)

	f.converter.answer(http.StatusTooManyRequests)
	f.requestPreview("case-1", "sheet.ods")
	before := f.converter.conversions()
	f.queue.run(t, f.app)
	require.Equal(t, 1, f.queue.size(), "a busy converter is retried, not failed")
	require.True(t, f.preview("case-1", "sheet.ods").Rendering)
	f.converter.answer(http.StatusOK)
	f.queue.run(t, f.app)
	require.Equal(t, before+2, f.converter.conversions())
	require.Equal(t, domain.PreviewShowCopy, f.preview("case-1", "sheet.ods").Verdict.Mode)

	f.converter.answer(http.StatusBadRequest)
	f.converter.mu.Lock()
	f.converter.pdf = ""
	f.converter.mu.Unlock()
	f.requestPreview("case-1", "letter.odt")
	f.queue.run(t, f.app)
	require.Equal(t, domain.PreviewRefusalConverterRefused, f.preview("case-1", "letter.odt").Verdict.Refusal)
}

// A rendering over its limit is a recorded failure; a file refused at
// admission is never queued nor recorded.
func TestRenderingLimitsAndAdmission(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.Preview.RenderedPDFBytes = 4 })
	space := f.open("case-1")
	f.uploadAs("case-1", space.SpaceToken, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.uploadAs("case-1", space.SpaceToken, "notes.docx", "", "plain text, renamed", false)
	f.requestPreview("case-1", "letter.docx")
	f.queue.run(t, f.app)
	require.Equal(t, domain.PreviewRefusalRenderingTooLarge, f.preview("case-1", "letter.docx").Verdict.Refusal)

	refused := f.requestPreview("case-1", "notes.docx")
	require.Equal(t, domain.PreviewRefusalStructureMismatch, refused.Verdict.Refusal)
	require.False(t, refused.Rendering)
	require.Zero(t, f.queue.size())
	require.Len(t, previewEntries(t, f), 1, "only the rendering's failure is recorded")
}

// Copies and failures lapse at retention: they stop showing, and the hourly
// sweep removes them by reading the previews directory alone.
func TestPreviewsLapse(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.Preview.Retention = "1h" })
	space := f.open("case-1")
	f.uploadAs("case-1", space.SpaceToken, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.uploadAs("case-1", space.SpaceToken, "letter.odt", "", fixtureFile(t, "letter.odt"), false)
	f.uploadAs("case-1", space.SpaceToken, "sheet.ods", "", fixtureFile(t, "sheet.ods"), false)
	f.requestPreview("case-1", "letter.docx")
	f.requestPreview("case-1", "letter.odt")
	f.queue.run(t, f.app)
	f.converter.answer(http.StatusBadRequest)
	f.requestPreview("case-1", "sheet.ods")
	f.queue.run(t, f.app)
	require.Equal(t, domain.PreviewRefusalConverterRefused, f.preview("case-1", "sheet.ods").Verdict.Refusal)
	require.Len(t, previewEntries(t, f), 3)
	old := time.Now().Add(-2 * time.Hour)
	for _, name := range previewEntries(t, f) {
		if strings.HasSuffix(name, ".failed") {
			require.NoError(t, os.Chtimes(filepath.Join(f.persistent, "assets/previews", name), old, old))
		}
	}
	require.Equal(t, domain.PreviewRender, f.preview("case-1", "sheet.ods").Verdict.Mode, "a lapsed failure no longer refuses")
	require.NoError(t, f.app.SweepPreviewCopies(t.Context()))
	require.Len(t, previewEntries(t, f), 2, "the lapsed failure is removed")
	f.converter.answer(http.StatusOK)

	lapsed := filepath.Join(f.persistent, "assets/previews", previewEntries(t, f)[0])
	require.NoError(t, os.Chtimes(lapsed, old, old))
	require.NoError(t, f.app.SweepPreviewCopies(t.Context()))
	require.Len(t, previewEntries(t, f), 1, "only the lapsed copy is removed")
	require.Len(t, f.files("case-1"), 3, "originals are untouched")

	kept := filepath.Join(f.persistent, "assets/previews", previewEntries(t, f)[0])
	require.NoError(t, os.Chtimes(kept, old, old))
	remaining := strings.TrimSuffix(filepath.Base(kept), ".pdf")
	for name, file := range f.files("case-1") {
		if file.Attributes.ContentDigest == remaining {
			require.Equal(t, domain.PreviewRender, f.preview("case-1", strings.TrimPrefix(name, "attachment/")).Verdict.Mode, "a lapsed copy no longer shows, before the sweep too")
		}
	}
}
