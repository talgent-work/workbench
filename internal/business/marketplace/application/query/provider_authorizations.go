package query

import "context"

// PersonalAuthorization is a credential-free reference to a configured record.
// Inactive records remain selectable so reauthorization can restore the same ID.
type PersonalAuthorization struct {
	ID, Provider, Status string
	SupportedTargets     []string
}

type ProviderAuthorizationQueryRepository interface {
	ListPersonalAuthorizations(context.Context, string) ([]PersonalAuthorization, error)
}
