// Package messagehandler receives the Integration Messages Agent consumes.
package messagehandler

import (
	"context"
	"fmt"

	integrationmessage "github.com/go-jimu/components/ddd/message"

	marketplaceintegrationv1 "github.com/talgent-work/contracts/gen/marketplace/integration/v1"
)

// CredentialChanges follows a credential change of a Provider Authorization.
type CredentialChanges interface {
	CredentialChanged(ctx context.Context, authorizationID string) error
}

// CredentialChangedHandler consumes Marketplace's Credential Change Published
// Fact. Every kind of change is handled alike: the Works using the
// authorization receive whatever Marketplace supplies now.
type CredentialChangedHandler struct {
	changes CredentialChanges
}

func NewCredentialChangedHandler(changes CredentialChanges) *CredentialChangedHandler {
	return &CredentialChangedHandler{changes: changes}
}

func (h *CredentialChangedHandler) Listening() []integrationmessage.Kind {
	return []integrationmessage.Kind{integrationmessage.KindOf(&marketplaceintegrationv1.CredentialChangedV1{})}
}

func (h *CredentialChangedHandler) Handle(ctx context.Context, msg integrationmessage.Message) error {
	payload, ok := msg.Payload().(*marketplaceintegrationv1.CredentialChangedV1)
	if !ok {
		return fmt.Errorf("agent credential changed: unexpected payload %T", msg.Payload())
	}
	return h.changes.CredentialChanged(ctx, payload.GetAuthorizationId())
}
