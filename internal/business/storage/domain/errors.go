package domain

import "errors"

var (
	ErrOwnerRequired          = errors.New("storage: owner is required")
	ErrInvalidOwner           = errors.New("storage: owner must be one visible path element")
	ErrInvalidFileKind        = errors.New("storage: invalid file kind")
	ErrInvalidFileName        = errors.New("storage: invalid file name")
	ErrSpaceNotOpen           = errors.New("storage: File Space is not open")
	ErrSpaceTokenRejected     = errors.New("storage: Space Token rejected")
	ErrShareLinkNotFound      = errors.New("storage: Share Link not found")
	ErrShareLinkRevoked       = errors.New("storage: Share Link revoked")
	ErrSharerRequired         = errors.New("storage: Share Link creator is required")
	ErrFileWriterRequired     = errors.New("storage: a file write must name its person or Work")
	ErrConcurrentModification = errors.New("storage: concurrent modification")
)
