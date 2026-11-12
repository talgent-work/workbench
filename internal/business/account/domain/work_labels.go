package domain

import (
	"fmt"
	"maps"
	"slices"
	"unicode/utf8"
)

// Bounds of the Work Labels an Agent Account keeps: Agent defines the labels
// themselves, Account only bounds what it stores and signs.
const (
	maxWorkLabels           = 16
	maxWorkLabelKeyLength   = 64
	maxWorkLabelValueLength = 256
)

// WorkLabel is one key=value pair of a Work's Work Labels.
type WorkLabel struct {
	Key, Value string
}

// WorkLabels are a Work's Work Labels as Agent supplies them, one pair per
// key, ordered by key. Account copies them into the Work's Identity Tokens
// and decides nothing from them.
type WorkLabels struct {
	pairs []WorkLabel
}

// NewWorkLabels bounds the supplied labels; nil or empty means none.
func NewWorkLabels(labels map[string]string) (WorkLabels, error) {
	if len(labels) > maxWorkLabels {
		return WorkLabels{}, fmt.Errorf("%w: at most %d work labels", ErrInvalidAgentAccount, maxWorkLabels)
	}
	result := WorkLabels{}
	for _, key := range slices.Sorted(maps.Keys(labels)) {
		value := labels[key]
		if key == "" || utf8.RuneCountInString(key) > maxWorkLabelKeyLength || value == "" || utf8.RuneCountInString(value) > maxWorkLabelValueLength {
			return WorkLabels{}, fmt.Errorf("%w: a work label exceeds its bounds", ErrInvalidAgentAccount)
		}
		result.pairs = append(result.pairs, WorkLabel{Key: key, Value: value})
	}
	return result, nil
}

// Map is the labels as key to value; nil for none.
func (l WorkLabels) Map() map[string]string {
	if len(l.pairs) == 0 {
		return nil
	}
	labels := make(map[string]string, len(l.pairs))
	for _, pair := range l.pairs {
		labels[pair.Key] = pair.Value
	}
	return labels
}

func (l WorkLabels) Equal(other WorkLabels) bool { return slices.Equal(l.pairs, other.pairs) }
