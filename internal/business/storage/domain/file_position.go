package domain

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

// maxFileNameBytes bounds a name to what one directory entry holds.
const maxFileNameBytes = 255

// FilePosition is a file's identity: its Directory and its name, one path
// element directly in the Directory. It compares by value.
type FilePosition struct {
	Directory DirectoryID
	Name      string
}

// NewFilePosition validates a position. A name is one directory entry: it is
// never a path, never "." or "..", and holds no control character, so a
// position can never point outside its Directory.
func NewFilePosition(directory DirectoryID, name string) (FilePosition, error) {
	if err := ValidateFileName(name); err != nil {
		return FilePosition{}, err
	}
	return FilePosition{Directory: directory, Name: name}, nil
}

// Renamed is the position of the same Directory under another name.
func (p FilePosition) Renamed(name string) (FilePosition, error) {
	return NewFilePosition(p.Directory, name)
}

// ValidateFileName checks that a name is a single, displayable directory entry.
func ValidateFileName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > maxFileNameBytes || !utf8.ValidString(name) {
		return ErrInvalidFileName
	}
	if strings.TrimSpace(name) != name {
		return ErrInvalidFileName
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return ErrInvalidFileName
		}
	}
	return nil
}

// ErrInvalidContentPath means a path is not a clean relative path within a
// Directory's whole content.
var ErrInvalidContentPath = errors.New("storage: invalid content file path")

// ContentPath is a clean relative path of a file within a Directory's whole
// content, such as "pages/payments.md".
type ContentPath string

// ParseContentPath admits a clean relative path, so a file never escapes its
// content. A path whose first element is hidden is kept for the writer and
// left out of a mounted listing.
func ParseContentPath(raw string) (ContentPath, error) {
	if !cleanRelative(raw) {
		return "", ErrInvalidContentPath
	}
	return ContentPath(raw), nil
}

// MountPoint is a clean relative path inside a Directory where its caller
// mounts another Directory.
type MountPoint string

// ParseMountPoint admits a clean relative path below the Directory's root.
func ParseMountPoint(raw string) (MountPoint, error) {
	if !cleanRelative(raw) {
		return "", ErrInvalidMountPoint
	}
	return MountPoint(raw), nil
}

// cleanRelative reports whether raw is a clean path strictly below a root.
func cleanRelative(raw string) bool {
	return raw != "" && !strings.ContainsAny(raw, "\\\x00") && !path.IsAbs(raw) && path.Clean(raw) == raw &&
		raw != "." && raw != ".." && !strings.HasPrefix(raw, "../")
}
