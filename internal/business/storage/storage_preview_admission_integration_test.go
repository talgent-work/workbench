//go:build integration

package storage

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func fixtureFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("infrastructure/testdata/preview", name))
	require.NoError(t, err)
	return string(content)
}

func (f *fixture) preview(ownerID, name string) application.FilePreview {
	f.t.Helper()
	preview, err := f.app.PreviewFile(f.t.Context(), ownerID, domain.FileKindAttachment, name)
	require.NoError(f.t, err)
	return preview
}

// Storage decides how each opened file is shown from its extension and
// content, never its declared type, and records nothing about the decision.
func TestPreviewIsDecidedFromExtensionAndContent(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.Preview.TextBytes = 1024 })
	space := f.open("case-1")
	f.uploadAs("case-1", space.SpaceToken, "letter.docx", "", fixtureFile(t, "letter.docx"), false)
	f.uploadAs("case-1", space.SpaceToken, "notes.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "plain text, renamed", false)
	f.uploadAs("case-1", space.SpaceToken, "page.log", "text/html", "<div><script>alert(1)</script></div>", false)
	f.uploadAs("case-1", space.SpaceToken, "long.md", "text/markdown", strings.Repeat("中", 600), false)
	f.uploadAs("case-1", space.SpaceToken, "tool.bin", "text/plain", "\x7fELF\x00\x00", false)

	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewRender, Display: domain.PreviewDisplayPDF}, f.preview("case-1", "letter.docx").Verdict)
	require.Nil(t, f.preview("case-1", "letter.docx").Link, "nothing is readable before a rendering")
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewRefused, Refusal: domain.PreviewRefusalStructureMismatch}, f.preview("case-1", "notes.docx").Verdict, "the declared type never routes a file")
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewRefused, Refusal: domain.PreviewRefusalUnsupported}, f.preview("case-1", "tool.bin").Verdict, "the declared text/plain does not make a binary text")

	log := f.preview("case-1", "page.log")
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewDirect, Display: domain.PreviewDisplayText}, log.Verdict)
	status, body, header := f.get(log.Link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "<div><script>alert(1)</script></div>", body)
	require.True(t, strings.HasPrefix(header.Get("Content-Disposition"), "inline"), "a preview reads inline")
	require.Contains(t, header.Get("Content-Security-Policy"), "sandbox")

	long := f.preview("case-1", "long.md")
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewTruncated, Display: domain.PreviewDisplayText, LimitBytes: 1024}, long.Verdict)

	_, err := os.Stat(filepath.Join(f.persistent, "assets/previews"))
	require.ErrorIs(t, err, os.ErrNotExist, "admission records nothing")

	stale := f.preview("case-1", "page.log")
	f.uploadAs("case-1", space.SpaceToken, "page.log", "", "\x00now binary", true)
	status, _, _ = f.get(stale.Link.URL)
	require.Equal(t, http.StatusConflict, status, "a preview link never serves content replaced after its verdict")
	_, err = f.app.PreviewFile(t.Context(), "case-1", domain.FileKindAttachment, "absent.md")
	require.ErrorIs(t, err, application.ErrFileNotFound)
}

// A download returns the original bytes under the original name and type.
func TestDownloadsKeepTheOriginal(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	deck := fixtureFile(t, "letter.docx")
	const docx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	f.uploadAs("case-1", space.SpaceToken, "季度 报告.docx", docx, deck, false)
	f.uploadAs("case-1", space.SpaceToken, "scan.pdf", "", "%PDF-1.7 body", false)
	f.uploadAs("case-1", space.SpaceToken, "data.unknownext", "", "\x00\x01\x02", false)

	download := func(name string, attachment bool) (string, http.Header) {
		link, err := f.app.IssueAccessLink(t.Context(), "case-1", domain.FileKindAttachment, name, attachment)
		require.NoError(t, err)
		status, body, header := f.get(link.URL)
		require.Equal(t, http.StatusOK, status)
		return body, header
	}

	body, header := download("季度 报告.docx", true)
	require.Equal(t, deck, body, "the original bytes")
	require.Equal(t, docx, header.Get("Content-Type"), "the recorded type")
	require.Equal(t, `attachment; filename*=utf-8''%E5%AD%A3%E5%BA%A6%20%E6%8A%A5%E5%91%8A.docx`, header.Get("Content-Disposition"))

	_, header = download("季度 报告.docx", false)
	require.True(t, strings.HasPrefix(header.Get("Content-Disposition"), "inline"))

	_, header = download("scan.pdf", true)
	require.Equal(t, "application/pdf", header.Get("Content-Type"), "inferred from the extension when none was recorded")

	body, header = download("data.unknownext", true)
	require.Equal(t, "\x00\x01\x02", body)
	require.Equal(t, "application/octet-stream", header.Get("Content-Type"))
	require.Equal(t, `attachment; filename=data.unknownext`, header.Get("Content-Disposition"), "an unknown type never renames the file")
}
