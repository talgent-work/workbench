package query

import "time"

// PluginResult is one selected Plugin's preparation outcome in the Work's
// current run, with the release whose declarations the Work last took and the
// version its Harness loaded, which may be newer. Installation and loading
// are independent, each unknown until reported.
type PluginResult struct {
	Slug, ReleaseVersion, Source      string
	LoadedVersion                     string
	InstallationReported, Installed   bool
	LoadingReported, Loaded           bool
	InstallationReason, LoadingReason string
	UpdatedAt                         time.Time
}
