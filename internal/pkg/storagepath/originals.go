// Package storagepath is Storage's Originals Mount Contract: the layout of
// the standing read-only mount of every JuiceFS Directory, by Directory
// owner, name and file path, and the extended attributes kept with each
// file. The reader interprets owners itself and reads originals in place by
// this layout alone. Beside it only the Access Link endpoint is published,
// which the native API serves to Works.
package storagepath

import (
	"errors"
	"path"
	"strings"
)

// OriginalsSubPath is where the originals mount lies within the JuiceFS
// volume.
const OriginalsSubPath = "talgent/assets/directories"

// OwnerFact is one condition of a Directory owner with the locating caller's
// value for it.
type OwnerFact struct {
	Condition string
	Value     string
}

// Owner writes a Directory owner as its one path element: each fact as
// condition=value, facts joined by ",", in the order given, and in both every
// byte other than ASCII letters, digits, "-", ".", "_", ":" and "@" written
// as %XX in upper-case hex.
func Owner(facts []OwnerFact) string {
	var element strings.Builder
	for i, fact := range facts {
		if i > 0 {
			element.WriteByte(',')
		}
		escape(&element, fact.Condition)
		element.WriteByte('=')
		escape(&element, fact.Value)
	}
	return element.String()
}

// ErrInvalidOwner means a path element is not a Directory owner written by
// Owner.
var ErrInvalidOwner = errors.New("storagepath: not a Directory owner element")

// ParseOwner reads the facts of an owner element Owner wrote.
func ParseOwner(element string) ([]OwnerFact, error) {
	if element == "" {
		return nil, ErrInvalidOwner
	}
	var facts []OwnerFact
	for _, written := range strings.Split(element, ",") {
		condition, value, ok := strings.Cut(written, "=")
		if !ok {
			return nil, ErrInvalidOwner
		}
		fact := OwnerFact{}
		var err error
		if fact.Condition, err = unescape(condition); err != nil {
			return nil, err
		}
		if fact.Value, err = unescape(value); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	if Owner(facts) != element {
		return nil, ErrInvalidOwner
	}
	return facts, nil
}

// Directory is a Directory's folder relative to the originals mount root:
// its owner element, then its name.
func Directory(owner []OwnerFact, name string) string {
	return path.Join(Owner(owner), name)
}

const upperHex = "0123456789ABCDEF"

func unreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-._:@", c) >= 0
}

func escape(element *strings.Builder, value string) {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if unreserved(c) {
			element.WriteByte(c)
			continue
		}
		element.WriteByte('%')
		element.WriteByte(upperHex[c>>4])
		element.WriteByte(upperHex[c&0x0f])
	}
}

func unescape(written string) (string, error) {
	var value strings.Builder
	for i := 0; i < len(written); i++ {
		c := written[i]
		if c != '%' {
			value.WriteByte(c)
			continue
		}
		if i+2 >= len(written) {
			return "", ErrInvalidOwner
		}
		high, low := strings.IndexByte(upperHex, written[i+1]), strings.IndexByte(upperHex, written[i+2])
		if high < 0 || low < 0 {
			return "", ErrInvalidOwner
		}
		value.WriteByte(byte(high<<4 | low))
		i += 2
	}
	return value.String(), nil
}

// Extended attributes kept with each file. Values are UTF-8 text; an absent
// attribute means the fact was not recorded.
const (
	// AttributeUploader is the account id of the human who uploaded an Attachment.
	AttributeUploader = "user.talgent.uploader"
	// AttributeWork is the Work that published an Artifact.
	AttributeWork = "user.talgent.work"
	// AttributeContentType is the media type declared at upload.
	AttributeContentType = "user.talgent.content_type"
)

// ContentPath is where Storage serves Access Links, on the platform's
// public address and on the native API alike.
const ContentPath = "/v1/storage/content"
