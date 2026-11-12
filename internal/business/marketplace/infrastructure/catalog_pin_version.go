package infrastructure

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// pinnedVersionLength is how many hex characters of a commit or archive digest
// name a Plugin release that declares no version: 48 bits, so two pins of one
// Plugin practically never collide, while the version stays short to display.
// Identity, not order: the current release is the one the latest catalog
// generation selects, never the greatest version.
const pinnedVersionLength = 12

// errLocalOnlySource marks an entry whose source runs a command on the
// installing machine. Such a package can never be acquired or retained by
// the platform, so the entry is skipped rather than failed.
var errLocalOnlySource = errors.New("marketplace: plugin source runs only on a local machine")

// localOnlySourceKinds are the catalog source kinds that name a local command
// instead of content. Any other unrecognized kind still fails the entry.
var localOnlySourceKinds = map[string]bool{"command": true}

func entrySourceKind(entry json.RawMessage) string {
	var value struct {
		Source json.RawMessage `json:"source"`
	}
	var origin struct {
		Kind string `json:"source"`
	}
	if json.Unmarshal(entry, &value) != nil || json.Unmarshal(value.Source, &origin) != nil {
		return ""
	}
	return origin.Kind
}

// pinnedVersion names the release of a Plugin that declares no version by the
// exact content its pinned entry fixes: the commit of a git package, the
// SHA-256 of an archive, or the resolved version of an npm package.
func pinnedVersion(pinned json.RawMessage) (string, error) {
	var value struct {
		Source struct {
			Kind    string `json:"source"`
			SHA     string `json:"sha"`
			SHA256  string `json:"sha256"`
			Version string `json:"version"`
		} `json:"source"`
	}
	if json.Unmarshal(pinned, &value) != nil {
		return "", errors.New("marketplace: invalid entry")
	}
	origin := value.Source
	switch {
	case (origin.Kind == "github" || origin.Kind == "url" || origin.Kind == "git-subdir") && gitCommit.MatchString(origin.SHA):
		return strings.ToLower(origin.SHA[:pinnedVersionLength]), nil
	case origin.Kind == "archive" && archiveDigest.MatchString(origin.SHA256):
		return strings.ToLower(origin.SHA256[:pinnedVersionLength]), nil
	case origin.Kind == "npm" && origin.Version != "":
		return origin.Version, nil
	}
	return "", errors.New("marketplace: the package pin names no version")
}

// withVersion states the version in the published entry, so the clients that
// load the Plugin report the release version the platform selected.
func withVersion(entry json.RawMessage, version string) (json.RawMessage, error) {
	var native map[string]json.RawMessage
	if e := json.Unmarshal(entry, &native); e != nil {
		return nil, e
	}
	native["version"], _ = json.Marshal(version)
	return json.Marshal(native)
}

// unchangedPin reports whether a retained release came from exactly this
// pinned package, so it can be reused without fetching the package again.
func unchangedPin(pinned json.RawMessage, previous []byte, previousVersion string) bool {
	version, e := pinnedVersion(pinned)
	if e != nil || version != previousVersion {
		return false
	}
	var a, b struct {
		Source any `json:"source"`
	}
	return json.Unmarshal(pinned, &a) == nil && json.Unmarshal(previous, &b) == nil && a.Source != nil && reflect.DeepEqual(a.Source, b.Source)
}
