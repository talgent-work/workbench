package domain

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrInvalidMappedPath means a path is not a mapped path.
	ErrInvalidMappedPath = errors.New("storage: invalid mapped path")
	// ErrMappingOutsidePrefix means a mapped path does not lie strictly
	// beneath its caller's mapping prefix, or the caller has none.
	ErrMappingOutsidePrefix = errors.New("storage: the mapped path lies outside the caller's mapping prefix")
	// ErrMappingOverlaps means a mapped path is, lies inside or lies above
	// another mapping's path.
	ErrMappingOverlaps = errors.New("storage: the mapped path overlaps another mapping")
	// ErrGrantedReadWrite means the Directory was once granted read-write,
	// so it is never mapped.
	ErrGrantedReadWrite = errors.New("storage: a Directory once granted read-write is never mapped")
	// ErrBackendNotMappable means the Directory is not on the Mapped View's
	// backend.
	ErrBackendNotMappable = errors.New("storage: only a JuiceFS Directory is mapped")
	// ErrDirectoryMapped means a read-write Mount Grant was asked for a
	// mapped Directory.
	ErrDirectoryMapped = errors.New("storage: a mapped Directory is only mounted read-only")
)

// MappedPath is a clean relative path in the Mapped View, chosen and
// interpreted only by the caller that maps there: at most
// MaxMappedPathCharacters, "/"-separated elements, none empty, "." or "..",
// none starting with "." or with or ending in white space, and none holding
// "\\" or NUL.
type MappedPath string

// MaxMappedPathCharacters bounds a mapped path, as its record keeps it.
const MaxMappedPathCharacters = 512

// ParseMappedPath admits a mapped path.
func ParseMappedPath(raw string) (MappedPath, error) {
	if !cleanRelative(raw) || !utf8.ValidString(raw) || utf8.RuneCountInString(raw) > MaxMappedPathCharacters {
		return "", ErrInvalidMappedPath
	}
	for element := range strings.SplitSeq(raw, "/") {
		if strings.HasPrefix(element, ".") || strings.TrimFunc(element, unicode.IsSpace) != element {
			return "", ErrInvalidMappedPath
		}
	}
	return MappedPath(raw), nil
}

// Beneath reports whether the path lies strictly beneath prefix.
func (p MappedPath) Beneath(prefix MappedPath) bool {
	return prefix != "" && strings.HasPrefix(string(p), string(prefix)+"/")
}

// Overlaps reports whether the paths are the same or one lies inside the
// other.
func (p MappedPath) Overlaps(other MappedPath) bool {
	return p == other || p.Beneath(other) || other.Beneath(p)
}

// MappingPrefix is the mapped path beneath which configuration lets one
// caller map; the zero value lets it map nowhere.
type MappingPrefix struct{ path MappedPath }

// NewMappingPrefix admits a configured prefix; an empty one maps nowhere.
func NewMappingPrefix(raw string) (MappingPrefix, error) {
	if raw == "" {
		return MappingPrefix{}, nil
	}
	path, err := ParseMappedPath(raw)
	if err != nil {
		return MappingPrefix{}, err
	}
	return MappingPrefix{path: path}, nil
}

// Admit refuses a path not strictly beneath the prefix.
func (p MappingPrefix) Admit(path MappedPath) error {
	if !path.Beneath(p.path) {
		return ErrMappingOutsidePrefix
	}
	return nil
}
