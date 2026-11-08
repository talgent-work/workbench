package domain

import "errors"

var (
	// ErrInvalidLocator means a Directory Locator or its application is not
	// one Storage keeps: a Work lifetime without the work condition, owner
	// facts out of condition order or repeated, an empty fact, or a name that
	// is not one visible path element.
	ErrInvalidLocator = errors.New("storage: invalid Directory Locator")
	// ErrDirectoryNotOpen means the Directory was never opened, or a Space
	// Token names none.
	ErrDirectoryNotOpen = errors.New("storage: the Directory was never opened")
	// ErrDirectoryNotAllocated means the Directory's space is not yet known to
	// exist on its backend.
	ErrDirectoryNotAllocated = errors.New("storage: the Directory's allocation has not completed")
	// ErrDirectoryReclaimed means the Directory was reclaimed: it is never
	// mounted, written or recreated.
	ErrDirectoryReclaimed = errors.New("storage: the Directory was reclaimed")
	// ErrPermanentDirectory means a permanent Directory was asked to be
	// reclaimed.
	ErrPermanentDirectory = errors.New("storage: a permanent Directory is never reclaimed")
	// ErrInvalidMountPoint means a mount point is not a clean relative path
	// inside its Directory.
	ErrInvalidMountPoint      = errors.New("storage: invalid mount point")
	ErrInvalidFileName        = errors.New("storage: invalid file name")
	ErrSpaceTokenRejected     = errors.New("storage: Space Token rejected")
	ErrShareLinkNotFound      = errors.New("storage: Share Link not found")
	ErrShareLinkRevoked       = errors.New("storage: Share Link revoked")
	ErrSharerRequired         = errors.New("storage: Share Link creator is required")
	ErrFileWriterRequired     = errors.New("storage: a file write must name its person or Work")
	ErrConcurrentModification = errors.New("storage: concurrent modification")
)
