package query

// DeclaredProvider is a Provider as the deployment declares it, with the
// owner's connection to it.
type DeclaredProvider struct {
	Name, DisplayName       string
	Targets                 []ProviderTarget
	TargetCredentialsExpire bool
	ObservesAccessScope     bool
	Connection              *ProviderConnectionState
}

// ProviderTarget is one destination of a Provider's credentials and the
// host patterns it reaches.
type ProviderTarget struct {
	Name  string
	Hosts []string
}
