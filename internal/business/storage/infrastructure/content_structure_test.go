package infrastructure

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Real files written by LibreOffice (and one encrypted by msoffcrypto-tool)
// pass through inspection and Preview Policy as a reader opens them.
func TestInspectedOfficeFilesAreAdmittedByStructure(t *testing.T) {
	read := func(name string) []byte { return previewFixture(t, name) }
	admit := func(name string, content []byte) domain.PreviewVerdict {
		structure, err := inspectContent(bytes.NewReader(content), int64(len(content)))
		require.NoError(t, err)
		return domain.AdmitForPreview(domain.PreviewSubject{Source: domain.PreviewOfSpaceFile, Name: name, SizeBytes: int64(len(content)), Structure: structure}, domain.DefaultPreviewLimits())
	}
	render := domain.PreviewVerdict{Mode: domain.PreviewRender, Display: domain.PreviewDisplayPDF}
	mismatch := domain.PreviewVerdict{Mode: domain.PreviewRefused, Refusal: domain.PreviewRefusalStructureMismatch}

	for _, name := range []string{"letter.docx", "letter.odt", "letter.doc", "letter.rtf", "sheet.xlsx", "sheet.ods", "sheet.xls", "deck.pptx", "deck.odp"} {
		require.Equal(t, render, admit(name, read(name)), name)
	}

	docx := read("letter.docx")
	require.Equal(t, mismatch, admit("notes.docx", []byte("plain text, renamed")), "text renamed to .docx")
	require.Equal(t, mismatch, admit("letter.docx", docx[:len(docx)/2]), "a truncated ZIP")
	require.Equal(t, mismatch, admit("sheet.docx", read("sheet.xlsx")), "an xlsx renamed to .docx")
	require.Equal(t, mismatch, admit("letter.ods", read("letter.odt")), "an ODF mimetype against the extension")
	require.Equal(t, mismatch, admit("sheet.doc", read("sheet.xls")), "a workbook renamed to .doc")
	require.Equal(t, mismatch, admit("letter.rtf", []byte("not rich text")), "text renamed to .rtf")
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewRefused, Refusal: domain.PreviewRefusalEncrypted}, admit("encrypted.docx", read("encrypted.docx")))
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewRefused, Refusal: domain.PreviewRefusalUnsupported}, admit("letter.bin", docx), "a binary of no previewable kind")
}

// Leading bytes handed to Storage show the structure Preview Policy judges,
// read no further than the text probe.
func TestInspectHeadFindsTheStructureOfLeadingBytes(t *testing.T) {
	inspector := ContentInspector{}
	png := inspector.InspectHead([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	require.Equal(t, "image/png", png.DetectedType)
	long := bytes.Repeat([]byte("a"), domain.TextProbeBytes+10)
	require.Len(t, inspector.InspectHead(long).Head, domain.TextProbeBytes)
}
