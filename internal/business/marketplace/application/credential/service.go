package credential

import (
	"context"
	"time"
)

// ServiceMaterial remains inside trusted transport and outbound injection.
type ServiceMaterial struct {
	Token     string
	ExpiresAt time.Time
}

type ServiceCredentialSupplier interface {
	SupplyServiceCredential(ctx context.Context, ownerUserID, authorizationID, target string) (ServiceMaterial, error)
}
