package domain

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Bounds of the Work Labels an Agent Account keeps: Agent defines the labels
// themselves, Account only bounds what it stores and signs.
const (
	maxWorkLabels           = 16
	maxWorkLabelKeyLength   = 64
	maxWorkLabelValueLength = 256
)

// WorkLabels are a Work's Work Labels as Agent supplies them. Account copies
// them into the Work's Identity Tokens and decides nothing from them. They
// are held in one canonical form, so equal labels compare equal and the
// Work's facts stay comparable as a whole (Facts never drift).
type WorkLabels struct {
	canonical string
}

// NewWorkLabels bounds the supplied labels; nil or empty means none.
func NewWorkLabels(labels map[string]string) (WorkLabels, error) {
	if len(labels) > maxWorkLabels {
		return WorkLabels{}, fmt.Errorf("%w: at most %d work labels", ErrInvalidAgentAccount, maxWorkLabels)
	}
	if len(labels) == 0 {
		return WorkLabels{}, nil
	}
	for key, value := range labels {
		if key == "" || utf8.RuneCountInString(key) > maxWorkLabelKeyLength || value == "" || utf8.RuneCountInString(value) > maxWorkLabelValueLength {
			return WorkLabels{}, fmt.Errorf("%w: a work label exceeds its bounds", ErrInvalidAgentAccount)
		}
	}
	// A map of strings always encodes, with its keys sorted.
	canonical, _ := json.Marshal(labels)
	return WorkLabels{canonical: string(canonical)}, nil
}

// Map is the labels as key to value; nil for none.
func (l WorkLabels) Map() map[string]string {
	if l.canonical == "" {
		return nil
	}
	var labels map[string]string
	_ = json.Unmarshal([]byte(l.canonical), &labels) // NewWorkLabels encoded it.
	return labels
}
