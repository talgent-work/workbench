package query

import (
	"context"
	"time"
)

type PluginResultReader interface {
	Read(context.Context, string) ([]PluginResult, error)
}

// PluginResult contains observation facts only, outside the Work aggregate.
type PluginResult struct {
	Slug, ReleaseVersion, Source    string
	InstallationObserved, Installed bool
	LoadObserved, Loaded            bool
	InstallationReason, LoadReason  string
	UpdatedAt                       time.Time
}
