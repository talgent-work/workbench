package domain

import (
	"encoding/hex"
	"errors"
)

// ErrInvalidPreviewKey means a Preview Copy key is not a content digest.
var ErrInvalidPreviewKey = errors.New("storage: a preview copy is keyed by a SHA-256 content digest")

// PreviewKey identifies the Preview Copy, or the recorded failure, of one
// content: its SHA-256 digest alone. Keyed by content rather than position,
// a copy never shows a replaced file's old content as the new one, and files
// holding the same content share it.
type PreviewKey struct {
	Digest string
}

func NewPreviewKey(digest string) (PreviewKey, error) {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest {
		return PreviewKey{}, ErrInvalidPreviewKey
	}
	return PreviewKey{Digest: digest}, nil
}
