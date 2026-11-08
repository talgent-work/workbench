package query

import "time"

// PluginResult is one selected Plugin's preparation outcome in the Work's
// current run, with the release the Work selected. Installation and loading
// are independent, each unknown until reported.
type PluginResult struct {
	Slug, ReleaseVersion, Source      string
	InstallationReported, Installed   bool
	LoadingReported, Loaded           bool
	InstallationReason, LoadingReason string
	UpdatedAt                         time.Time
}
