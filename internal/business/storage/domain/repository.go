package domain

import (
	"context"
	"io"
)

// DirectoryRepository is the collection of Directories with their Share
// Links.
type DirectoryRepository interface {
	// Get returns ErrDirectoryNotOpen for a Directory never opened.
	Get(ctx context.Context, id DirectoryID) (*Directory, error)
	// GetBySpaceToken returns the Directory the Space Token was issued for,
	// or ErrSpaceTokenRejected.
	GetBySpaceToken(ctx context.Context, token string) (*Directory, error)
	// GetByShareToken returns the Directory holding the Share Link, or
	// ErrShareLinkNotFound.
	GetByShareToken(ctx context.Context, token string) (*Directory, error)
	// Save inserts a new Directory, or updates a loaded one under its
	// Version; a concurrent change returns ErrConcurrentModification. A new
	// Directory whose identity the backends cannot lay out returns
	// ErrInvalidLocator.
	Save(ctx context.Context, directory *Directory) error
}

// PreviewCopyRepository is the collection of Preview Copies with their
// renderings.
type PreviewCopyRepository interface {
	// Get returns ErrPreviewCopyNotFound for a content without one.
	Get(ctx context.Context, key PreviewKey) (*PreviewCopy, error)
	// Add keeps a new Preview Copy in Rendering; one already kept for the
	// content returns ErrConcurrentModification.
	Add(ctx context.Context, copy *PreviewCopy) error
	// Save keeps a settled Preview Copy, with its rendering when Rendered.
	Save(ctx context.Context, copy *PreviewCopy, rendering io.Reader) error
	// Remove removes the Preview Copy and its rendering; an absent one is
	// already removed.
	Remove(ctx context.Context, key PreviewKey) error
	// OpenRendering opens a Rendered Preview Copy's PDF.
	OpenRendering(ctx context.Context, key PreviewKey) (io.ReadSeekCloser, int64, error)
}
