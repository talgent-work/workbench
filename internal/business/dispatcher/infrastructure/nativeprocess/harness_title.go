package nativeprocess

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// TitleMarker precedes, as a JSON string, the title the Harness gave the
// Work's conversation in the systemMessage of the Work's Stop hook. The
// Harness keeps the title in its transcript only; Agent's launch installs the
// hook (execution.titleHook) and the Harness emits its systemMessage as an
// informational system message, its text after a label of its own.
const TitleMarker = "talgent-title:"

// maxTitleLength bounds a reported title, as Agent bounds a display title.
const maxTitleLength = 200

// ReportedTitle is the title an informational system message carries from the
// Stop hook, or "" when the message is none or carries no usable title.
func ReportedTitle(message *Message) string {
	if message.Type != "system" || message.Subtype != "informational" || len(message.Content) == 0 {
		return ""
	}
	var content string
	if json.Unmarshal(message.Content, &content) != nil {
		return ""
	}
	_, encoded, found := strings.Cut(content, TitleMarker)
	if !found {
		return ""
	}
	// The title is the first JSON value after the marker; the label and
	// anything the Harness appends stay outside it.
	var title string
	if json.NewDecoder(strings.NewReader(encoded)).Decode(&title) != nil {
		return ""
	}
	title = strings.Join(strings.Fields(strings.ToValidUTF8(title, "")), " ")
	if utf8.RuneCountInString(title) > maxTitleLength {
		title = strings.TrimSpace(string([]rune(title)[:maxTitleLength]))
	}
	return title
}

// ObserveTitle records the title the process reported and tells whether it
// differs from the one it reported before.
func (s *ProtocolState) ObserveTitle(title string) bool {
	if title == "" || s.Title == title {
		return false
	}
	s.Title = title
	return true
}
