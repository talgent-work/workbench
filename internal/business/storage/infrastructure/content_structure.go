package infrastructure

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"

	"github.com/gabriel-vasile/mimetype"
	"github.com/richardlehane/mscfb"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// odfMimetypeBytes bounds the ODF mimetype entry read; the longest ODF type
// is well under it.
const odfMimetypeBytes = 128

// inspectContent finds what Preview Policy judges in a file's content: its
// start, the type the content shows, and a ZIP's entries or a compound file's
// streams. Unreadable archives yield no entries rather than an error, since
// that is itself the structure judged.
func inspectContent(content io.ReaderAt, size int64) (domain.ContentStructure, error) {
	head := make([]byte, min(size, domain.TextProbeBytes))
	if _, err := content.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return domain.ContentStructure{}, err
	}
	detected := mimetype.Detect(head)
	structure := domain.ContentStructure{Head: head, DetectedType: detected.String()}
	switch {
	case descendsFrom(detected, "application/zip"):
		structure.ZipEntries = zipEntries(content, size)
	case descendsFrom(detected, "application/x-ole-storage"):
		structure.CompoundStreams = compoundStreams(content)
	}
	return structure, nil
}

// ContentInspector finds the content structure of leading bytes Storage is
// handed, as inspectContent finds it in a file it holds.
type ContentInspector struct{}

func (ContentInspector) InspectHead(head []byte) domain.ContentStructure {
	head = head[:min(len(head), domain.TextProbeBytes)]
	// Reading from memory cannot fail.
	structure, _ := inspectContent(bytes.NewReader(head), int64(len(head)))
	return structure
}

func descendsFrom(detected *mimetype.MIME, parent string) bool {
	for m := detected; m != nil; m = m.Parent() {
		if m.Is(parent) {
			return true
		}
	}
	return false
}

func zipEntries(content io.ReaderAt, size int64) []domain.ZipEntry {
	archive, err := zip.NewReader(content, size)
	if err != nil {
		return nil
	}
	entries := make([]domain.ZipEntry, 0, len(archive.File))
	for i, file := range archive.File {
		entry := domain.ZipEntry{Name: file.Name, Stored: file.Method == zip.Store}
		if i == 0 && file.Name == "mimetype" && entry.Stored {
			entry.Content = readEntry(file)
		}
		entries = append(entries, entry)
	}
	return entries
}

func readEntry(file *zip.File) []byte {
	opened, err := file.Open()
	if err != nil {
		return nil
	}
	defer opened.Close()
	content, err := io.ReadAll(io.LimitReader(opened, odfMimetypeBytes))
	if err != nil {
		return nil
	}
	return content
}

func compoundStreams(content io.ReaderAt) []string {
	document, err := mscfb.New(content)
	if err != nil {
		return nil
	}
	var streams []string
	for _, file := range document.File {
		if len(file.Path) == 0 {
			streams = append(streams, file.Name)
		}
	}
	return streams
}
