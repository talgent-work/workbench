package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func zipOf(names ...string) []ZipEntry {
	entries := make([]ZipEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, ZipEntry{Name: name})
	}
	return entries
}

func odfOf(mimetype string, stored bool) []ZipEntry {
	return append([]ZipEntry{{Name: "mimetype", Stored: stored, Content: []byte(mimetype)}}, zipOf("content.xml")...)
}

var (
	docxStructure = ContentStructure{Head: []byte("PK\x03\x04"), ZipEntries: zipOf("[Content_Types].xml", "_rels/.rels", "word/document.xml")}
	xlsxStructure = ContentStructure{Head: []byte("PK\x03\x04"), ZipEntries: zipOf("[Content_Types].xml", "xl/workbook.xml")}
	pptxStructure = ContentStructure{Head: []byte("PK\x03\x04"), ZipEntries: zipOf("[Content_Types].xml", "ppt/presentation.xml")}
	encrypted     = ContentStructure{Head: []byte("\xd0\xcf\x11\xe0"), CompoundStreams: []string{"EncryptionInfo", "EncryptedPackage", "\x06DataSpaces"}}
	plainText     = ContentStructure{Head: []byte("just some text"), DetectedType: "text/plain; charset=utf-8"}
)

func TestAdmitForPreviewOfficeStructure(t *testing.T) {
	limits := DefaultPreviewLimits()
	render := PreviewVerdict{Mode: PreviewRender, Display: PreviewDisplayPDF}
	mismatch := PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalStructureMismatch}
	cases := []struct {
		name      string
		file      string
		structure ContentStructure
		want      PreviewVerdict
	}{
		{"docx", "report.docx", docxStructure, render},
		{"xlsx", "data.xlsx", xlsxStructure, render},
		{"pptx", "deck.pptx", pptxStructure, render},
		{"upper-case extension", "DECK.PPTX", pptxStructure, render},
		{"plain text renamed to docx", "notes.docx", plainText, mismatch},
		{"truncated zip has no readable entries", "report.docx", ContentStructure{Head: []byte("PK\x03\x04")}, mismatch},
		{"xlsx renamed to docx", "data.docx", xlsxStructure, mismatch},
		{"zip without content types", "report.docx", ContentStructure{ZipEntries: zipOf("word/document.xml")}, mismatch},
		{"encrypted docx", "secret.docx", encrypted, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalEncrypted}},
		{"encrypted pptx", "secret.pptx", encrypted, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalEncrypted}},
		{"odt", "letter.odt", ContentStructure{ZipEntries: odfOf("application/vnd.oasis.opendocument.text", true)}, render},
		{"ods", "sheet.ods", ContentStructure{ZipEntries: odfOf("application/vnd.oasis.opendocument.spreadsheet", true)}, render},
		{"odp", "slides.odp", ContentStructure{ZipEntries: odfOf("application/vnd.oasis.opendocument.presentation", true)}, render},
		{"odf mimetype against extension", "letter.odt", ContentStructure{ZipEntries: odfOf("application/vnd.oasis.opendocument.spreadsheet", true)}, mismatch},
		{"odf mimetype compressed", "letter.odt", ContentStructure{ZipEntries: odfOf("application/vnd.oasis.opendocument.text", false)}, mismatch},
		{"odf mimetype not first", "letter.odt", ContentStructure{ZipEntries: append(zipOf("content.xml"), odfOf("application/vnd.oasis.opendocument.text", true)...)}, mismatch},
		{"doc", "old.doc", ContentStructure{CompoundStreams: []string{"WordDocument", "1Table"}}, render},
		{"xls workbook", "old.xls", ContentStructure{CompoundStreams: []string{"Workbook"}}, render},
		{"xls book (BIFF5)", "old.xls", ContentStructure{CompoundStreams: []string{"Book"}}, render},
		{"ppt", "old.ppt", ContentStructure{CompoundStreams: []string{"PowerPoint Document", "Current User"}}, render},
		{"xls renamed to doc", "old.doc", ContentStructure{CompoundStreams: []string{"Workbook"}}, mismatch},
		{"encrypted compound is not an encrypted doc", "old.doc", encrypted, mismatch},
		{"rtf", "memo.rtf", ContentStructure{Head: []byte(`{\rtf1\ansi hello}`)}, render},
		{"text renamed to rtf", "memo.rtf", plainText, mismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AdmitForPreview(PreviewSubject{Source: PreviewOfSpaceFile, Name: tc.file, SizeBytes: 1024, Structure: tc.structure}, limits)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAdmitForPreviewLimits(t *testing.T) {
	limits := DefaultPreviewLimits()
	png := ContentStructure{Head: []byte("\x89PNG"), DetectedType: "image/png"}
	pdf := ContentStructure{Head: []byte("%PDF-1.7")}
	text := ContentStructure{Head: []byte("# title")}
	cases := []struct {
		name      string
		file      string
		size      int64
		structure ContentStructure
		want      PreviewVerdict
	}{
		{"office at limit", "deck.pptx", limits.OfficeSource, pptxStructure, PreviewVerdict{Mode: PreviewRender, Display: PreviewDisplayPDF}},
		{"office over limit", "deck.pptx", limits.OfficeSource + 1, pptxStructure, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalTooLarge, LimitBytes: limits.OfficeSource}},
		{"pdf at limit", "a.pdf", limits.NativePDF, pdf, PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayPDF}},
		{"pdf over limit", "a.pdf", limits.NativePDF + 1, pdf, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalTooLarge, LimitBytes: limits.NativePDF}},
		{"image at limit", "a.png", limits.Image, png, PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayImage}},
		{"image over limit", "a.png", limits.Image + 1, png, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalTooLarge, LimitBytes: limits.Image}},
		{"text at limit", "a.md", limits.Text, text, PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayText}},
		{"text over limit truncates", "a.md", limits.Text + 1, text, PreviewVerdict{Mode: PreviewTruncated, Display: PreviewDisplayText, LimitBytes: limits.Text}},
		{"html at limit renders", "a.html", limits.Markup, text, PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayText}},
		{"html over limit shows source", "a.html", limits.Markup + 1, text, PreviewVerdict{Mode: PreviewTruncated, Display: PreviewDisplayText, LimitBytes: limits.Markup}},
		{"svg over limit shows source", "a.svg", limits.Markup + 1, text, PreviewVerdict{Mode: PreviewTruncated, Display: PreviewDisplayText, LimitBytes: limits.Markup}},
		{"pdf that is not a pdf", "a.pdf", 10, text, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalStructureMismatch}},
		{"image that is not an image", "a.png", 10, plainText, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalStructureMismatch}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AdmitForPreview(PreviewSubject{Source: PreviewOfSpaceFile, Name: tc.file, SizeBytes: tc.size, Structure: tc.structure}, limits)
			require.Equal(t, tc.want, got)
		})
	}

	t.Run("a changed limit changes the verdict", func(t *testing.T) {
		subject := PreviewSubject{Source: PreviewOfSpaceFile, Name: "deck.pptx", SizeBytes: 5 * mebibyte, Structure: pptxStructure}
		require.Equal(t, PreviewRender, AdmitForPreview(subject, limits).Mode)
		tighter := limits
		tighter.OfficeSource = 4 * mebibyte
		require.Equal(t, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalTooLarge, LimitBytes: 4 * mebibyte}, AdmitForPreview(subject, tighter))
	})
}

func TestAdmitForPreviewText(t *testing.T) {
	limits := DefaultPreviewLimits()
	admit := func(name, head string, size int64) PreviewVerdict {
		return AdmitForPreview(PreviewSubject{Source: PreviewOfSpaceFile, Name: name, SizeBytes: size, Structure: ContentStructure{Head: []byte(head)}}, limits)
	}
	direct := PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayText}
	unsupported := PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalUnsupported}

	require.Equal(t, direct, admit("server.log", "<div>not a page</div>", 22))
	require.Equal(t, direct, admit("LICENSE", "MIT", 3))
	require.Equal(t, direct, admit("empty.txt", "", 0))
	require.Equal(t, direct, admit("中文.md", "# 标题", 8))
	require.Equal(t, unsupported, admit("tool.bin", "ELF\x00\x01", 5), "a NUL means binary")
	require.Equal(t, unsupported, admit("latin1.txt", "caf\xe9 au lait", 13), "invalid UTF-8 means binary")
	require.Equal(t, unsupported, admit("archive.zip", "PK\x03\x04\x14\x00", 6))

	cut := strings.Repeat("a", TextProbeBytes-1) + "\xe4" // the first byte of 中, cut by the probe
	require.Equal(t, PreviewVerdict{Mode: PreviewTruncated, Display: PreviewDisplayText, LimitBytes: limits.Text}, admit("long.txt", cut, limits.Text+1), "a character cut by the probe still reads as text")
	require.Equal(t, unsupported, admit("short.txt", "ab\xe4", 3), "an incomplete character at the real end is not text")
	nulLate := strings.Repeat("a", TextProbeBytes) + "\x00"
	require.Equal(t, direct, admit("late.txt", nulLate, int64(len(nulLate))), "only the first 8 KiB are probed")
}

func TestAdmitForPreviewOutcomes(t *testing.T) {
	limits := DefaultPreviewLimits()
	subject := PreviewSubject{Source: PreviewOfSpaceFile, Name: "deck.pptx", SizeBytes: 1024, Structure: pptxStructure}

	subject.Outcome = PreviewOutcome{Copy: true}
	require.Equal(t, PreviewVerdict{Mode: PreviewShowCopy, Display: PreviewDisplayPDF}, AdmitForPreview(subject, limits))
	subject.Outcome = PreviewOutcome{Failure: PreviewRefusalCorrupt}
	require.Equal(t, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalCorrupt}, AdmitForPreview(subject, limits))

	subject.Outcome = PreviewOutcome{Copy: true}
	subject.SizeBytes = limits.OfficeSource + 1
	require.Equal(t, PreviewRefusalTooLarge, AdmitForPreview(subject, limits).Refusal, "admission is judged before any existing copy")

	workspace := PreviewSubject{Source: PreviewOfWorkspaceFile, Name: "deck.pptx", SizeBytes: 1024, Structure: pptxStructure}
	require.Equal(t, PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalUnsupported}, AdmitForPreview(workspace, limits), "workspace files are never rendered")
	workspace.Name = "notes.md"
	workspace.Structure = plainText
	require.Equal(t, PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayText}, AdmitForPreview(workspace, limits))
}

func TestPreviewLimitsWithDefaults(t *testing.T) {
	limits := PreviewLimits{Text: 2 * mebibyte}.WithDefaults()
	require.Equal(t, int64(2*mebibyte), limits.Text)
	require.Equal(t, DefaultPreviewLimits().OfficeSource, limits.OfficeSource)
}

func TestSettleRendering(t *testing.T) {
	limits := DefaultPreviewLimits()
	cases := []struct {
		name   string
		result ConverterResult
		want   RenderingSettlement
	}{
		{"rendered", ConverterResult{Outcome: ConverterRendered, PDFBytes: 1024}, RenderingSettlement{Copy: true}},
		{"rendered at the limit", ConverterResult{Outcome: ConverterRendered, PDFBytes: limits.RenderedPDF}, RenderingSettlement{Copy: true}},
		{"rendered over the limit", ConverterResult{Outcome: ConverterRendered, PDFBytes: limits.RenderedPDF + 1}, RenderingSettlement{Failure: PreviewRefusalRenderingTooLarge}},
		{"corrupt", ConverterResult{Outcome: ConverterCorrupt}, RenderingSettlement{Failure: PreviewRefusalCorrupt}},
		{"encrypted", ConverterResult{Outcome: ConverterEncrypted}, RenderingSettlement{Failure: PreviewRefusalEncrypted}},
		{"refused", ConverterResult{Outcome: ConverterRefused}, RenderingSettlement{Failure: PreviewRefusalConverterRefused}},
		{"busy", ConverterResult{Outcome: ConverterBusy}, RenderingSettlement{}},
		{"timed out", ConverterResult{Outcome: ConverterTimedOut}, RenderingSettlement{}},
		{"unavailable", ConverterResult{Outcome: ConverterUnavailable}, RenderingSettlement{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SettleRendering(tc.result, limits)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.want == RenderingSettlement{}, got.Temporary())
		})
	}
}

// A workspace file is judged without Preview Copies, and its reader is
// served at most the shown length or its kind's limit.
func TestWorkspaceFileVerdictAndReadLimit(t *testing.T) {
	limits := DefaultPreviewLimits()
	limits.Markup = limits.Text / 2
	shown := func(mode PreviewMode, display PreviewDisplay, limit int64) PreviewVerdict {
		return PreviewVerdict{Mode: mode, Display: display, LimitBytes: limit}
	}
	pdf, png := ContentStructure{Head: []byte("%PDF-1.7")}, ContentStructure{Head: []byte("\x89PNG"), DetectedType: "image/png"}
	text := func(head string) ContentStructure { return ContentStructure{Head: []byte(head)} }
	for _, c := range []struct {
		name      string
		size      int64
		structure ContentStructure
		want      PreviewVerdict
		readLimit int64
	}{
		{"a.pdf", 10, pdf, shown(PreviewDirect, PreviewDisplayPDF, 0), limits.NativePDF},
		{"a.pdf", 10, text("nope"), refused(PreviewRefusalStructureMismatch), 0},
		{"a.pdf", limits.NativePDF + 1, pdf, tooLarge(limits.NativePDF), 0},
		{"a.png", 8, png, shown(PreviewDirect, PreviewDisplayImage, 0), limits.Image},
		{"a.png", 4, text("text"), refused(PreviewRefusalStructureMismatch), 0},
		{"a.docx", 4, docxStructure, refused(PreviewRefusalUnsupported), 0},
		{"a.html", limits.Markup + 1, text("<p>"), shown(PreviewTruncated, PreviewDisplayText, limits.Markup), limits.Markup},
		{"a.html", 3, text("<p>"), shown(PreviewDirect, PreviewDisplayText, 0), limits.Text},
		{"a.txt", limits.Text + 1, text("caf\xc3"), shown(PreviewTruncated, PreviewDisplayText, limits.Text), limits.Text},
		{"a.txt", 4, text("caf\xc3"), refused(PreviewRefusalUnsupported), 0},
	} {
		got := AdmitForPreview(PreviewSubject{Source: PreviewOfWorkspaceFile, Name: c.name, SizeBytes: c.size, Structure: c.structure}, limits)
		require.Equal(t, c.want, got, c.name)
		require.Equal(t, c.readLimit, got.ReadLimit(limits), c.name)
	}
	require.Equal(t, limits.RenderedPDF, shown(PreviewShowCopy, PreviewDisplayPDF, 0).ReadLimit(limits), "a Preview Copy is a rendered PDF")
}
