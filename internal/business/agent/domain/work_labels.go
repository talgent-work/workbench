package domain

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidWorkLabels rejects Work Labels outside their limits.
var ErrInvalidWorkLabels = errors.New("agent: invalid work labels")

// Work Label limits: how many pairs a Work carries and how long each key and
// value is.
const (
	MaxWorkLabels           = 16
	MaxWorkLabelKeyLength   = 64
	MaxWorkLabelValueLength = 256
)

// WorkLabel is one key=value pair of a Work's Work Labels.
type WorkLabel struct {
	Key, Value string
}

// WorkLabels are the key=value pairs a Work's starter gives it at creation,
// whose keys and values the starter alone defines. A Work keeps them for good
// (Labels fixed), and they never grant or limit access to it (Labels grant
// nothing). The pairs are ordered by key, one per key.
type WorkLabels struct {
	pairs []WorkLabel
}

// NewWorkLabels validates the starter's pairs: at most MaxWorkLabels; each
// key 1 to MaxWorkLabelKeyLength characters of letters, digits, '.', '_',
// '-' and '/', starting with a letter or digit; each value 1 to
// MaxWorkLabelValueLength characters on one line, without surrounding
// whitespace.
func NewWorkLabels(pairs map[string]string) (WorkLabels, error) {
	if len(pairs) > MaxWorkLabels {
		return WorkLabels{}, fmt.Errorf("%w: at most %d labels", ErrInvalidWorkLabels, MaxWorkLabels)
	}
	labels := WorkLabels{pairs: make([]WorkLabel, 0, len(pairs))}
	for _, key := range slices.Sorted(maps.Keys(pairs)) {
		value := pairs[key]
		if !validWorkLabelKey(key) {
			return WorkLabels{}, fmt.Errorf("%w: key %q", ErrInvalidWorkLabels, truncate(key))
		}
		if !validWorkLabelValue(value) {
			return WorkLabels{}, fmt.Errorf("%w: value of %q", ErrInvalidWorkLabels, key)
		}
		labels.pairs = append(labels.pairs, WorkLabel{Key: key, Value: value})
	}
	return labels, nil
}

func validWorkLabelKey(key string) bool {
	if key == "" || len(key) > MaxWorkLabelKeyLength {
		return false
	}
	for i, r := range key {
		alphanumeric := r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r))
		if i == 0 && !alphanumeric {
			return false
		}
		if !alphanumeric && !strings.ContainsRune("._-/", r) {
			return false
		}
	}
	return true
}

func validWorkLabelValue(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxWorkLabelValueLength || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

// truncate keeps a rejected key short enough to quote in an error.
func truncate(key string) string {
	if len(key) <= MaxWorkLabelKeyLength {
		return key
	}
	return key[:MaxWorkLabelKeyLength] + "…"
}

// Pairs are the labels ordered by key.
func (l WorkLabels) Pairs() []WorkLabel { return slices.Clone(l.pairs) }

// Map is the labels as key to value; nil for none.
func (l WorkLabels) Map() map[string]string {
	if len(l.pairs) == 0 {
		return nil
	}
	pairs := make(map[string]string, len(l.pairs))
	for _, pair := range l.pairs {
		pairs[pair.Key] = pair.Value
	}
	return pairs
}

func (l WorkLabels) Empty() bool { return len(l.pairs) == 0 }

func (l WorkLabels) Equal(other WorkLabels) bool {
	return slices.EqualFunc(l.pairs, other.pairs, func(a, b WorkLabel) bool { return a == b })
}
