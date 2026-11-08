package query

import (
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkStartOptions contains only capabilities supported by the deployed runtime.
// It holds no endpoint credentials and is not a Session capability snapshot.
type WorkStartOptions struct {
	// Models are the currently provided Model Configurations in platform order;
	// the first is the default.
	Models              []WorkModelOption
	Plugins             []WorkPluginOption
	Connectors          []domain.ConnectorOption
	DefaultStartupAgent string
}

type WorkPluginOption struct {
	Name, DisplayName, Category, Description string
	Required                                 bool
	Agents, Skills                           []WorkCapabilityOption
}

type WorkCapabilityOption struct{ Name, Description, ArgumentHint string }

// WorkModelOption is one provided Model Configuration, presented by its name
// and its Model Template's Model Vendor, with that template's thinking levels.
type WorkModelOption struct {
	Selection            domain.WorkModelSelection
	Name                 string
	Vendor               string
	ThinkingLevels       []string
	DefaultThinkingLevel string
}

// WorkStartOptionsQuery contains business selection supplied by an authorized caller.
// WorkStartOptionsQuery reads the options of a retained Work, or of a new
// Work under a person's employee definition.
type WorkStartOptionsQuery struct {
	SubjectID, WorkID string
	EmployeeID        string
	// RequiredPlugins are what the caller's new Work requires beside the
	// definition's; they are offered as required.
	RequiredPlugins []string
}

// WorkStartRepositories are the repositories an admitted owner may check out
// in a new Work. Discovery asks the Provider, so it is read on its own.
type WorkStartRepositories struct {
	Repositories            []WorkRepositoryOption
	RepositoriesUnavailable bool
	// RecentRepositoryIDs are up to five reachable repositories from the owner's
	// own earlier Works, most recent first.
	RecentRepositoryIDs []string
}

type WorkRepositoryOption struct {
	ID, Provider, RepoName, DefaultBranch string
}
