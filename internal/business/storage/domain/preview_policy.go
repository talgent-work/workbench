package domain

import (
	"bytes"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// Preview Policy is Storage's stateless decision of how a file is shown to
// readers. It judges only the facts it is given: the file's name and size,
// the structure the caller found in its content, and the outcome already
// recorded for that content. Nothing it decides is recorded, so a changed
// rule or limit applies at the next opening.

// PreviewLimits are the preview limits of Storage's preview configuration.
type PreviewLimits struct {
	// OfficeSource bounds an office file that is rendered.
	OfficeSource int64
	// RenderedPDF bounds the PDF a rendering produces.
	RenderedPDF int64
	// NativePDF bounds a PDF shown as it is.
	NativePDF int64
	// Image bounds an image shown as it is.
	Image int64
	// Text is how much of a text file is shown; a longer one is truncated.
	Text int64
	// Markup bounds an HTML or SVG file that is rendered; a longer one shows
	// its truncated source.
	Markup int64
}

const mebibyte = 1 << 20

// DefaultPreviewLimits are the conservative starting limits.
func DefaultPreviewLimits() PreviewLimits {
	return PreviewLimits{
		OfficeSource: 20 * mebibyte, RenderedPDF: 50 * mebibyte, NativePDF: 50 * mebibyte,
		Image: 20 * mebibyte, Text: mebibyte, Markup: mebibyte,
	}
}

// WithDefaults fills every unset limit with its default.
func (l PreviewLimits) WithDefaults() PreviewLimits {
	defaults := DefaultPreviewLimits()
	for _, limit := range []struct{ value, fallback *int64 }{
		{&l.OfficeSource, &defaults.OfficeSource}, {&l.RenderedPDF, &defaults.RenderedPDF}, {&l.NativePDF, &defaults.NativePDF},
		{&l.Image, &defaults.Image}, {&l.Text, &defaults.Text}, {&l.Markup, &defaults.Markup},
	} {
		if *limit.value <= 0 {
			*limit.value = *limit.fallback
		}
	}
	return l
}

// PreviewSource tells where the previewed file lives: File Space files
// may have Preview Copies, Session Workspace files never do.
type PreviewSource int

const (
	PreviewOfSpaceFile PreviewSource = iota + 1
	PreviewOfWorkspaceFile
)

// ContentStructure is what the caller found in a file's content.
type ContentStructure struct {
	// Head is the start of the content, at most TextProbeBytes.
	Head []byte
	// DetectedType is the media type the content itself shows.
	DetectedType string
	// ZipEntries are a ZIP archive's entries in archive order; nil when the
	// content is not a readable ZIP.
	ZipEntries []ZipEntry
	// CompoundStreams are a compound file's top-level stream names; nil when
	// the content is not a readable compound file.
	CompoundStreams []string
}

// TextProbeBytes is how much of the content the text judgement reads.
const TextProbeBytes = 8 << 10

// ZipEntry is one ZIP entry. Content is kept only for an ODF mimetype entry.
type ZipEntry struct {
	Name    string
	Stored  bool
	Content []byte
}

// PreviewOutcome is what a rendering already left for the content.
type PreviewOutcome struct {
	// Copy says a Preview Copy exists.
	Copy bool
	// Failure is the recorded failure's reason; PreviewRefusalNone if none.
	Failure PreviewRefusal
}

// PreviewSubject is the file a preview is decided for.
type PreviewSubject struct {
	Source    PreviewSource
	Name      string
	SizeBytes int64
	Structure ContentStructure
	Outcome   PreviewOutcome
}

// PreviewMode is how a file is shown.
type PreviewMode int

const (
	PreviewRefused PreviewMode = iota + 1
	// PreviewShowCopy shows the content's Preview Copy.
	PreviewShowCopy
	// PreviewRender needs a Preview Copy rendered first.
	PreviewRender
	// PreviewDirect shows the file as it is.
	PreviewDirect
	// PreviewTruncated shows the start of the file as text.
	PreviewTruncated
)

// PreviewDisplay is what the reader receives to show.
type PreviewDisplay int

const (
	PreviewDisplayNone PreviewDisplay = iota
	PreviewDisplayText
	PreviewDisplayPDF
	PreviewDisplayImage
)

// PreviewRefusal is the reason a reader can recognize for a file not shown.
type PreviewRefusal int

const (
	PreviewRefusalNone PreviewRefusal = iota
	// PreviewRefusalUnsupported: no preview exists for this kind of file.
	PreviewRefusalUnsupported
	// PreviewRefusalStructureMismatch: the content is not what the
	// extension says.
	PreviewRefusalStructureMismatch
	// PreviewRefusalEncrypted: the file is encrypted.
	PreviewRefusalEncrypted
	// PreviewRefusalTooLarge: the file is over its kind's limit.
	PreviewRefusalTooLarge
	// PreviewRefusalCorrupt: the converter found the content damaged.
	PreviewRefusalCorrupt
	// PreviewRefusalConverterRefused: the converter refused the content.
	PreviewRefusalConverterRefused
	// PreviewRefusalRenderingTooLarge: the rendering is over its limit.
	PreviewRefusalRenderingTooLarge
)

// PreviewVerdict is how a file is shown. LimitBytes is the shown length of a
// truncated file, or the limit a too large file exceeds.
type PreviewVerdict struct {
	Mode       PreviewMode
	Display    PreviewDisplay
	Refusal    PreviewRefusal
	LimitBytes int64
}

// Readable says the verdict shows bytes the reader fetches.
func (v PreviewVerdict) Readable() bool {
	return v.Mode == PreviewShowCopy || v.Mode == PreviewDirect || v.Mode == PreviewTruncated
}

// ReadLimit is the most bytes a reader of the verdict is served: the shown
// length of a truncated file, otherwise its kind's limit, so content grown
// since the judgement is cut there; 0 when nothing is shown.
func (v PreviewVerdict) ReadLimit(limits PreviewLimits) int64 {
	switch {
	case !v.Readable():
		return 0
	case v.Mode == PreviewTruncated:
		return v.LimitBytes
	case v.Mode == PreviewShowCopy:
		return limits.RenderedPDF
	case v.Display == PreviewDisplayPDF:
		return limits.NativePDF
	case v.Display == PreviewDisplayImage:
		return limits.Image
	}
	return max(limits.Text, limits.Markup)
}

func refused(reason PreviewRefusal) PreviewVerdict {
	return PreviewVerdict{Mode: PreviewRefused, Refusal: reason}
}

func tooLarge(limit int64) PreviewVerdict {
	return PreviewVerdict{Mode: PreviewRefused, Refusal: PreviewRefusalTooLarge, LimitBytes: limit}
}

// officeRule recognizes the structure an office extension promises.
type officeRule func(ContentStructure) bool

var officeRules = map[string]officeRule{
	"docx": ooxml("word/document.xml"),
	"xlsx": ooxml("xl/workbook.xml"),
	"pptx": ooxml("ppt/presentation.xml"),
	"odt":  odf("application/vnd.oasis.opendocument.text"),
	"ods":  odf("application/vnd.oasis.opendocument.spreadsheet"),
	"odp":  odf("application/vnd.oasis.opendocument.presentation"),
	"doc":  compound("WordDocument"),
	"xls":  compound("Workbook", "Book"),
	"ppt":  compound("PowerPoint Document"),
	"rtf":  func(s ContentStructure) bool { return bytes.HasPrefix(s.Head, []byte(`{\rtf`)) },
}

// ooxmlExtensions are the OOXML kinds, which an encrypted file is stored as a
// compound file instead of a ZIP.
var ooxmlExtensions = map[string]bool{"docx": true, "xlsx": true, "pptx": true}

var imageExtensions = map[string]bool{"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "avif": true, "bmp": true, "ico": true}

func ooxml(mainPart string) officeRule {
	return func(s ContentStructure) bool {
		return hasZipEntry(s.ZipEntries, "[Content_Types].xml") && hasZipEntry(s.ZipEntries, mainPart)
	}
}

func odf(mimetype string) officeRule {
	return func(s ContentStructure) bool {
		if len(s.ZipEntries) == 0 {
			return false
		}
		first := s.ZipEntries[0]
		return first.Name == "mimetype" && first.Stored && string(first.Content) == mimetype
	}
}

func compound(streams ...string) officeRule {
	return func(s ContentStructure) bool {
		return slices.ContainsFunc(streams, func(name string) bool { return slices.Contains(s.CompoundStreams, name) })
	}
}

func hasZipEntry(entries []ZipEntry, name string) bool {
	return slices.ContainsFunc(entries, func(entry ZipEntry) bool { return entry.Name == name })
}

// encryptedOOXML is how an encrypted OOXML file is stored: a compound file
// holding the encryption header and the encrypted package.
func encryptedOOXML(s ContentStructure) bool {
	return slices.Contains(s.CompoundStreams, "EncryptionInfo") && slices.Contains(s.CompoundStreams, "EncryptedPackage")
}

// AdmitForPreview decides how a file is shown from its extension, size,
// content structure and the outcome recorded for its content. A declared
// type never routes it.
func AdmitForPreview(subject PreviewSubject, limits PreviewLimits) PreviewVerdict {
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(subject.Name)), ".")
	structure := subject.Structure
	if rule, office := officeRules[ext]; office {
		return admitOffice(subject, ext, rule, limits)
	}
	switch {
	case ext == "pdf":
		if subject.SizeBytes > limits.NativePDF {
			return tooLarge(limits.NativePDF)
		}
		if !bytes.HasPrefix(structure.Head, []byte("%PDF-")) {
			return refused(PreviewRefusalStructureMismatch)
		}
		return PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayPDF}
	case imageExtensions[ext]:
		if subject.SizeBytes > limits.Image {
			return tooLarge(limits.Image)
		}
		if !strings.HasPrefix(structure.DetectedType, "image/") {
			return refused(PreviewRefusalStructureMismatch)
		}
		return PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayImage}
	}
	if !isText(structure.Head, subject.SizeBytes) {
		return refused(PreviewRefusalUnsupported)
	}
	shown := limits.Text
	if ext == "html" || ext == "htm" || ext == "svg" {
		// Rendered markup must be whole; past its limit only source shows.
		if subject.SizeBytes > limits.Markup {
			return PreviewVerdict{Mode: PreviewTruncated, Display: PreviewDisplayText, LimitBytes: min(shown, limits.Markup)}
		}
		return PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayText}
	}
	if subject.SizeBytes > shown {
		return PreviewVerdict{Mode: PreviewTruncated, Display: PreviewDisplayText, LimitBytes: shown}
	}
	return PreviewVerdict{Mode: PreviewDirect, Display: PreviewDisplayText}
}

func admitOffice(subject PreviewSubject, ext string, rule officeRule, limits PreviewLimits) PreviewVerdict {
	if subject.Source != PreviewOfSpaceFile {
		return refused(PreviewRefusalUnsupported)
	}
	if subject.SizeBytes > limits.OfficeSource {
		return tooLarge(limits.OfficeSource)
	}
	if ooxmlExtensions[ext] && encryptedOOXML(subject.Structure) {
		return refused(PreviewRefusalEncrypted)
	}
	if !rule(subject.Structure) {
		return refused(PreviewRefusalStructureMismatch)
	}
	switch {
	case subject.Outcome.Copy:
		return PreviewVerdict{Mode: PreviewShowCopy, Display: PreviewDisplayPDF}
	case subject.Outcome.Failure != PreviewRefusalNone:
		return refused(subject.Outcome.Failure)
	}
	return PreviewVerdict{Mode: PreviewRender, Display: PreviewDisplayPDF}
}

// isText judges the content text when its first TextProbeBytes hold no NUL
// and are valid UTF-8; a character cut by the probe's end does not count.
func isText(head []byte, size int64) bool {
	if len(head) > TextProbeBytes {
		head = head[:TextProbeBytes]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	if int64(len(head)) < size {
		head = trimCutRune(head)
	}
	return utf8.Valid(head)
}

// trimCutRune drops an incomplete UTF-8 sequence at the end of a prefix.
func trimCutRune(head []byte) []byte {
	for back := 1; back <= utf8.UTFMax-1 && back <= len(head); back++ {
		b := head[len(head)-back]
		if !utf8.RuneStart(b) {
			continue
		}
		if !utf8.FullRune(head[len(head)-back:]) {
			return head[:len(head)-back]
		}
		break
	}
	return head
}

// ConverterOutcome is what one rendering attempt returned.
type ConverterOutcome int

const (
	// ConverterRendered: a PDF of PDFBytes came back.
	ConverterRendered ConverterOutcome = iota + 1
	// ConverterCorrupt: the converter could not read the content.
	ConverterCorrupt
	// ConverterEncrypted: the content is protected by a password.
	ConverterEncrypted
	// ConverterRefused: the converter refused the request as invalid.
	ConverterRefused
	// ConverterBusy: the converter had no capacity for it now.
	ConverterBusy
	// ConverterTimedOut: no answer within the attempt's time.
	ConverterTimedOut
	// ConverterUnavailable: the converter could not be reached or failed.
	ConverterUnavailable
)

// ConverterResult is one rendering attempt's result.
type ConverterResult struct {
	Outcome  ConverterOutcome
	PDFBytes int64
}

// RenderingSettlement is what a rendering attempt leaves: a Preview Copy, a
// recorded failure, or nothing at all for a temporary failure.
type RenderingSettlement struct {
	Copy    bool
	Failure PreviewRefusal
}

// Temporary says the attempt leaves nothing, so the next opening tries again.
func (s RenderingSettlement) Temporary() bool {
	return !s.Copy && s.Failure == PreviewRefusalNone
}

// SettleRendering turns a converter result into what it leaves behind. Only
// what the converter itself found about the content is recorded; being busy,
// slow or unreachable says nothing about the content.
func SettleRendering(result ConverterResult, limits PreviewLimits) RenderingSettlement {
	switch result.Outcome {
	case ConverterRendered:
		if result.PDFBytes > limits.RenderedPDF {
			return RenderingSettlement{Failure: PreviewRefusalRenderingTooLarge}
		}
		return RenderingSettlement{Copy: true}
	case ConverterCorrupt:
		return RenderingSettlement{Failure: PreviewRefusalCorrupt}
	case ConverterEncrypted:
		return RenderingSettlement{Failure: PreviewRefusalEncrypted}
	case ConverterRefused:
		return RenderingSettlement{Failure: PreviewRefusalConverterRefused}
	}
	return RenderingSettlement{}
}
