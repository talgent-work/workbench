package domain

import "errors"

var (
	// ErrContentChanged means a replacement expected a content that is no
	// longer current; it changed nothing.
	ErrContentChanged = errors.New("storage: the expected content is no longer current")
	// ErrInvalidContentRevision means an expected revision names no content.
	ErrInvalidContentRevision = errors.New("storage: an expected content revision must not be empty")
)

// ContentRevision identifies one content switched into a Directory replaced
// as a whole; it is empty before the first switch. Callers treat it as
// opaque.
type ContentRevision string

// ContentExpectation is the content a replacement expects to be current:
// any content, no content, or one revision.
type ContentExpectation struct {
	named    bool
	revision ContentRevision
}

// AnyContent expects nothing: the replacement switches over whichever
// content is current.
func AnyContent() ContentExpectation { return ContentExpectation{} }

// NoContent expects a Directory never replaced.
func NoContent() ContentExpectation { return ContentExpectation{named: true} }

// ExpectRevision expects the content a read or an earlier replacement
// reported.
func ExpectRevision(revision ContentRevision) (ContentExpectation, error) {
	if revision == "" {
		return ContentExpectation{}, ErrInvalidContentRevision
	}
	return ContentExpectation{named: true, revision: revision}, nil
}

// Admit lets a replacement proceed only while the content it expects is
// current, so a writer that made new content from what it read never
// switches over a content it did not read (Conditional replacement).
func (e ContentExpectation) Admit(current ContentRevision) error {
	if e.named && e.revision != current {
		return ErrContentChanged
	}
	return nil
}
