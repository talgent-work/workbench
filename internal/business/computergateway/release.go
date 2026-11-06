package computergateway

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/Masterminds/semver/v3"
)

// releaseVersion is a platform release as Auto Tag numbers it, without its v.
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Release answers which platform release this deployment runs: the Target
// Version every Managed Computer should run, the version of @talgent-work/computer
// the Computers page tells a person to install. GET /computer/v1/release is
// the single source of the Target Version; whatever upgrades a Managed
// Computer reads it there. It is public, as the npm package is.
type Release struct{ Version string }

func (r Release) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(struct {
		Version string `json:"version"`
	}{r.Version})
}

// minimumAboveTarget reports whether a Minimum Version lies above the Target
// Version. Each comes from another deployment's configuration, the Target
// from this service's release-version and the Minimum from Computer's
// minimum-version, so nothing keeps them in order: a machine that upgraded
// to such a Target is still below the Minimum and takes no new run. Either
// one empty, or not a release, is no such conflict.
func minimumAboveTarget(target, minimum string) bool {
	have, err := semver.StrictNewVersion(target)
	if err != nil {
		return false
	}
	want, err := semver.StrictNewVersion(minimum)
	if err != nil {
		return false
	}
	return have.LessThan(want)
}
