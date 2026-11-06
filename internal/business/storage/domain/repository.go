package domain

import "context"

// FileSpaceRepository is the collection of File Spaces with their Share Links.
type FileSpaceRepository interface {
	// Get returns ErrSpaceNotOpen for an owner whose space was never opened.
	Get(ctx context.Context, owner string) (*FileSpace, error)
	// GetBySpaceToken returns the space the Space Token was issued for, or
	// ErrSpaceTokenRejected.
	GetBySpaceToken(ctx context.Context, token string) (*FileSpace, error)
	// GetByShareToken returns the space holding the Share Link, or
	// ErrShareLinkNotFound.
	GetByShareToken(ctx context.Context, token string) (*FileSpace, error)
	// Save inserts a new space, or updates a loaded one under its Version;
	// a concurrent change returns ErrConcurrentModification.
	Save(ctx context.Context, space *FileSpace) error
}
