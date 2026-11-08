//go:build integration

package storage

import (
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

	"github.com/stretchr/testify/require"

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

var caseInputs = named("inputs", "subject", "case:1")

func (f *fixture) preview(directory application.Named, name string) application.FilePreview {
	f.t.Helper()
	preview, err := f.app.PreviewFile(f.t.Context(), at(directory, name))
	require.NoError(f.t, err)
	return preview
}

func (f *fixture) requestPreview(directory application.Named, name string) application.FilePreview {
	f.t.Helper()
	preview, err := f.app.RequestFilePreview(f.t.Context(), at(directory, name))
	require.NoError(f.t, err)
	return preview
}

func previewEntries(t *testing.T, f *fixture) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.persistent, juiceFSPreviews))
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
	token := f.open(fileSpace(caseInputs))
	docx := fixtureFile(t, "letter.docx")
	f.uploadAs(caseInputs, token, "letter.docx", "", docx, false)
	require.Zero(t, f.converter.conversions(), "writing a file renders nothing")
	require.Empty(t, previewEntries(t, f))

	opened := f.preview(caseInputs, "letter.docx")
	require.Equal(t, domain.PreviewRender, opened.Verdict.Mode)
	require.False(t, opened.Rendering, "asking how a file shows renders nothing")
	for range 3 {
		require.True(t, f.requestPreview(caseInputs, "letter.docx").Rendering)
	}
	require.Equal(t, 1, f.queue.size(applicationtask.RenderPreviewType), "one content is one rendering")
	require.True(t, f.preview(caseInputs, "letter.docx").Rendering, "the Preview Copy's state tells a rendering in progress")

	f.renderings()
	require.Equal(t, 1, f.converter.conversions())
	require.Equal(t, []string{"source.docx"}, f.converter.names, "no file name reaches the converter")
	shown := f.preview(caseInputs, "letter.docx")
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewShowCopy, Display: domain.PreviewDisplayPDF}, shown.Verdict)
	status, body, header := f.get(shown.Link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "%PDF-1.7 rendered", body)
	require.Equal(t, "application/pdf", header.Get("Content-Type"))
	require.True(t, strings.HasPrefix(header.Get("Content-Disposition"), "inline"))
	require.Equal(t, []string{f.files(caseInputs)["letter.docx"].Attributes.ContentDigest + ".pdf"}, previewEntries(t, f))

	link, err := f.app.IssueAccessLink(t.Context(), at(caseInputs, "letter.docx"), true)
	require.NoError(t, err)
	_, body, _ = f.get(link.URL)
	require.Equal(t, docx, body, "a download is always the original")

	// The same content elsewhere shares the copy.
	other := named("inputs", "subject", "case:2")
	f.uploadAs(other, f.open(fileSpace(other)), "copy.docx", "", docx, false)
	require.Equal(t, domain.PreviewShowCopy, f.preview(other, "copy.docx").Verdict.Mode)
	f.requestPreview(other, "copy.docx")
	require.Zero(t, f.queue.size(applicationtask.RenderPreviewType))
	require.Equal(t, 1, f.converter.conversions())
}

// A replaced file never shows its old content's copy, and deleting, replacing
// or renaming leaves copies to lapse.
func TestReplacedFileShowsOnlyItsNewContent(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	f.uploadAs(caseInputs, token, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.requestPreview(caseInputs, "letter.docx")
	f.renderings()
	require.Len(t, previewEntries(t, f), 1)

	// Bytes after a ZIP's end record keep it valid: new content, same kind.
	f.uploadAs(caseInputs, token, "letter.docx", "", fixtureFile(t, "letter.docx")+"\n", true)
	require.Equal(t, domain.PreviewRender, f.preview(caseInputs, "letter.docx").Verdict.Mode, "the old content's copy never shows")
	_, err := f.app.RenameFile(t.Context(), at(caseInputs, "letter.docx"), token, "renamed.docx", "user-1")
	require.NoError(t, err)
	require.Len(t, previewEntries(t, f), 1, "a rename leaves the copy")
	require.NoError(t, f.app.DeleteFile(t.Context(), at(caseInputs, "renamed.docx"), token, "user-1"))
	require.Len(t, previewEntries(t, f), 1, "a deletion leaves the copy to lapse")
}

// What the converter finds about the content is recorded and shown at the
// next opening without converting again; anything else leaves nothing.
func TestRenderingFailures(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	f.uploadAs(caseInputs, token, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.uploadAs(caseInputs, token, "letter.odt", "", fixtureFile(t, "letter.odt"), false)
	f.uploadAs(caseInputs, token, "sheet.ods", "", fixtureFile(t, "sheet.ods"), false)

	f.converter.answerWith(http.StatusBadRequest, "The document 'source.docx' is password-protected. Provide its password in the 'password' form field.")
	f.requestPreview(caseInputs, "letter.docx")
	f.renderings()
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewRefused, Refusal: domain.PreviewRefusalEncrypted}, f.preview(caseInputs, "letter.docx").Verdict)
	f.requestPreview(caseInputs, "letter.docx")
	require.Zero(t, f.queue.size(applicationtask.RenderPreviewType), "a recorded failure is not rendered again")

	f.converter.answer(http.StatusInternalServerError)
	require.True(t, f.requestPreview(caseInputs, "letter.odt").Rendering)
	f.renderings()
	after := f.preview(caseInputs, "letter.odt")
	require.Equal(t, domain.PreviewRender, after.Verdict.Mode, "a temporary failure leaves nothing")
	require.False(t, after.Rendering, "the abandoned rendering holds nothing back")

	f.converter.answer(http.StatusTooManyRequests)
	f.requestPreview(caseInputs, "sheet.ods")
	before := f.converter.conversions()
	f.renderings()
	require.Equal(t, 1, f.queue.size(applicationtask.RenderPreviewType), "a busy converter is retried, not failed")
	require.True(t, f.preview(caseInputs, "sheet.ods").Rendering)
	f.converter.answer(http.StatusOK)
	f.renderings()
	require.Equal(t, before+2, f.converter.conversions())
	require.Equal(t, domain.PreviewShowCopy, f.preview(caseInputs, "sheet.ods").Verdict.Mode)

	f.converter.answer(http.StatusBadRequest)
	f.converter.mu.Lock()
	f.converter.pdf = ""
	f.converter.mu.Unlock()
	f.requestPreview(caseInputs, "letter.odt")
	f.renderings()
	require.Equal(t, domain.PreviewRefusalConverterRefused, f.preview(caseInputs, "letter.odt").Verdict.Refusal)
}

// A rendering over its limit is a recorded failure; a file refused at
// admission is never queued nor recorded.
func TestRenderingLimitsAndAdmission(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.Preview.RenderedPDFBytes = 4 })
	token := f.open(fileSpace(caseInputs))
	f.uploadAs(caseInputs, token, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.uploadAs(caseInputs, token, "notes.docx", "", "plain text, renamed", false)
	f.requestPreview(caseInputs, "letter.docx")
	f.renderings()
	require.Equal(t, domain.PreviewRefusalRenderingTooLarge, f.preview(caseInputs, "letter.docx").Verdict.Refusal)

	refused := f.requestPreview(caseInputs, "notes.docx")
	require.Equal(t, domain.PreviewRefusalStructureMismatch, refused.Verdict.Refusal)
	require.False(t, refused.Rendering)
	require.Zero(t, f.queue.size(applicationtask.RenderPreviewType))
	require.Len(t, previewEntries(t, f), 1, "only the rendering's failure is recorded")
}

// Copies and failures lapse at retention: they stop showing, and the hourly
// Lapse Preview Copies task removes them by reading the preview area alone.
// A rendering past its deadline holds nothing back.
func TestPreviewCopiesLapse(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.Preview.Retention = "1h" })
	token := f.open(fileSpace(caseInputs))
	f.uploadAs(caseInputs, token, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.uploadAs(caseInputs, token, "letter.odt", "", fixtureFile(t, "letter.odt"), false)
	f.uploadAs(caseInputs, token, "sheet.ods", "", fixtureFile(t, "sheet.ods"), false)
	f.requestPreview(caseInputs, "letter.docx")
	f.requestPreview(caseInputs, "letter.odt")
	f.renderings()
	f.converter.answer(http.StatusBadRequest)
	f.requestPreview(caseInputs, "sheet.ods")
	f.renderings()
	require.Equal(t, domain.PreviewRefusalConverterRefused, f.preview(caseInputs, "sheet.ods").Verdict.Refusal)
	require.Len(t, previewEntries(t, f), 3)
	old := time.Now().Add(-2 * time.Hour)
	for _, name := range previewEntries(t, f) {
		if strings.HasSuffix(name, ".failed") {
			require.NoError(t, os.Chtimes(filepath.Join(f.persistent, juiceFSPreviews, name), old, old))
		}
	}
	require.NoError(t, f.app.LapsePreviewCopies(t.Context()))
	require.Len(t, previewEntries(t, f), 2, "the lapsed failure is removed")
	require.Equal(t, domain.PreviewRender, f.preview(caseInputs, "sheet.ods").Verdict.Mode, "a lapsed failure no longer refuses")

	lapsed := filepath.Join(f.persistent, juiceFSPreviews, previewEntries(t, f)[0])
	require.NoError(t, os.Chtimes(lapsed, old, old))
	require.NoError(t, f.app.LapsePreviewCopies(t.Context()))
	require.Len(t, previewEntries(t, f), 1, "only the lapsed copy is removed")
	require.Len(t, f.files(caseInputs), 3, "originals are untouched")

	// A rendering whose task died past its deadline is requested again.
	f.converter.answer(http.StatusOK)
	require.True(t, f.requestPreview(caseInputs, "sheet.ods").Rendering)
	stuck := filepath.Join(f.persistent, juiceFSPreviews, f.files(caseInputs)["sheet.ods"].Attributes.ContentDigest+".rendering")
	require.FileExists(t, stuck)
	require.NoError(t, os.Chtimes(stuck, old, old))
	require.False(t, f.preview(caseInputs, "sheet.ods").Rendering, "a rendering past its deadline is running no more")
	require.True(t, f.requestPreview(caseInputs, "sheet.ods").Rendering)
}
