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

// ProviderTarget is one destination of a Provider's credentials: the host
// patterns it reaches and the path patterns that narrow it there, none
// meaning every path.
type ProviderTarget struct {
	Name  string
	Hosts []string
	Paths []string
}
