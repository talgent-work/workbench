package query

import (
	"context"
	"time"
)

// PluginResultReader, a facet of ExecutionQueryRepository, reads the Plugin
// outcomes of a Work's current run: those its most recently begun
// AgentExecution recorded (Plugin result reading).
type PluginResultReader interface {
	ListCurrentPluginResults(ctx context.Context, workID string) ([]PluginResult, error)
}

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
