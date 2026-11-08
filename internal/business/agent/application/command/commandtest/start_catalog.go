package commandtest

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// PluginCatalog is the Plugin and Connector part of Marketplace's catalog.
type PluginCatalog interface {
	ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error)
	ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error)
	ResolvePlugin(context.Context, string, string) (domain.PluginRelease, error)
}

// PersonalConnections are the Provider Authorizations and repositories a
// user reaches.
type PersonalConnections interface {
	ListPersonalAuthorizations(context.Context, string) ([]domain.PersonalAuthorization, error)
	ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error)
}

// StartCatalog composes a test's stand-ins for the two parts of what
// Marketplace offers a Work's start; it supplies no credentials.
func StartCatalog(plugins PluginCatalog, connections PersonalConnections) command.MarketplacePort {
	return startCatalog{PluginCatalog: plugins, PersonalConnections: connections}
}

type startCatalog struct {
	PluginCatalog
	PersonalConnections
	NoCredentials
}

// ErrNoCredentials is the refusal of a Marketplace that supplies no
// credentials.
var ErrNoCredentials = errors.New("agent: this Marketplace supplies no credentials")

// NoCredentials is Marketplace's credential supply for a test whose Works
// never take a credential: it refuses every one.
type NoCredentials struct{}

func (NoCredentials) SupplyRepositoryCredential(context.Context, command.RepositoryCredentialRequest) (command.RepositoryCredentialMaterial, error) {
	return command.RepositoryCredentialMaterial{}, ErrNoCredentials
}

func (NoCredentials) SupplyServiceCredential(context.Context, command.ServiceCredentialRequest) (command.ServiceCredentialMaterial, error) {
	return command.ServiceCredentialMaterial{}, ErrNoCredentials
}
