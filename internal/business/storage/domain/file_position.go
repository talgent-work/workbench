// Package domain holds Storage's File Space and the File positions its Share
// Links point at, the rules of Reference Space content and of workspace
// paths, and Preview Policy. The files themselves are facts of persistent
// storage.
package domain

import (
	"strings"
	"unicode/utf8"
)

// FileKind tells an Attachment from an Artifact.
type FileKind string

const (
	FileKindAttachment FileKind = "attachment"
	FileKindArtifact   FileKind = "artifact"
)

// Valid reports whether the kind is one Storage keeps.
func (k FileKind) Valid() bool { return k == FileKindAttachment || k == FileKindArtifact }

// maxFileNameBytes bounds a name to what one directory entry holds.
const maxFileNameBytes = 255

// FilePosition is a file's identity: its File Space's owner, its kind and
// its name.
type FilePosition struct {
	Owner string
	Kind  FileKind
	Name  string
}

// NewFilePosition validates a position. A name is one directory entry: it is
// never a path, never "." or "..", and holds no control character, so a
// position can never point outside its space's directory of that kind.
func NewFilePosition(owner string, kind FileKind, name string) (FilePosition, error) {
	position := FilePosition{Owner: strings.TrimSpace(owner), Kind: kind, Name: name}
	if err := position.Validate(); err != nil {
		return FilePosition{}, err
	}
	return position, nil
}

// Validate checks the position's identity rules.
func (p FilePosition) Validate() error {
	if _, err := ParseOwner(p.Owner); err != nil {
		return err
	}
	if !p.Kind.Valid() {
		return ErrInvalidFileKind
	}
	return ValidateFileName(p.Name)
}

// Renamed is the position of the same space and kind under another name.
func (p FilePosition) Renamed(name string) (FilePosition, error) {
	return NewFilePosition(p.Owner, p.Kind, name)
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

// ParseOwner admits an owner identity a caller names a File Space or a
// Reference Space by. Storage never interprets it; it only keeps it one
// visible directory entry, so a space laid out by its owner stays one
// directory of its own.
func ParseOwner(raw string) (string, error) {
	owner := strings.TrimSpace(raw)
	if owner == "" {
		return "", ErrOwnerRequired
	}
	if strings.HasPrefix(owner, ".") || ValidateFileName(owner) != nil {
		return "", ErrInvalidOwner
	}
	return owner, nil
}
