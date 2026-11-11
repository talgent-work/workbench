package infrastructure

import (
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// A Directory's owner is written as one path element of Storage's own
// Directory layout, which is private to Storage: each fact as
// condition=value, facts joined by ",", in the owner's order, and in both
// every byte other than ASCII letters, digits, "-", ".", "_", ":" and "@"
// written as %XX in upper-case hex. The same element keys the owner in
// Storage's records.

// errInvalidOwnerElement means a stored element is not one ownerElement wrote.
var errInvalidOwnerElement = errors.New("storage: not a Directory owner element")

// ownerElement writes an owner's facts as their one path element.
func ownerElement(facts []domain.OwnerFact) string {
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

// parseOwnerElement reads the facts of an element ownerElement wrote.
func parseOwnerElement(element string) ([]domain.OwnerFact, error) {
	if element == "" {
		return nil, errInvalidOwnerElement
	}
	var facts []domain.OwnerFact
	for _, written := range strings.Split(element, ",") {
		condition, value, ok := strings.Cut(written, "=")
		if !ok {
			return nil, errInvalidOwnerElement
		}
		fact := domain.OwnerFact{}
		var err error
		if fact.Condition, err = unescape(condition); err != nil {
			return nil, err
		}
		if fact.Value, err = unescape(value); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	if ownerElement(facts) != element {
		return nil, errInvalidOwnerElement
	}
	return facts, nil
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
			return "", errInvalidOwnerElement
		}
		high, low := strings.IndexByte(upperHex, written[i+1]), strings.IndexByte(upperHex, written[i+2])
		if high < 0 || low < 0 {
			return "", errInvalidOwnerElement
		}
		value.WriteByte(byte(high<<4 | low))
		i += 2
	}
	return value.String(), nil
}
