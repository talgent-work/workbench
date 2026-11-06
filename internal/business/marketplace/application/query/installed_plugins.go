package query

import "context"

type InstalledPlugin struct {
	UserID             string
	Slug               string
	Enabled            bool
	Configuration      map[string]any
	Version            int
	RedactedParameters []string
}

type InstalledPluginPage struct {
	Plugins    []InstalledPlugin
	NextCursor string
}

type InstalledPluginQueryRepository interface {
	List(ctx context.Context, userID, afterSlug string, limit int) (InstalledPluginPage, error)
}
