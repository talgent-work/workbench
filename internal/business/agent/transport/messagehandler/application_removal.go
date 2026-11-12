package messagehandler

import (
	"context"
	"fmt"

	integrationmessage "github.com/go-jimu/components/ddd/message"

	accountintegrationv1 "github.com/talgent-work/contracts/gen/go/account/integration/v1"
)

// RemovedApplications closes the Works of an Application Account removed.
type RemovedApplications interface {
	ApplicationRemoved(ctx context.Context, applicationID string) error
}

// ApplicationRemovalHandler consumes Account's Application Removed Published
// Fact. The fact only names the Application: the Works still unclosed when it
// is handled are closed, so a repeated fact queues nothing twice.
type ApplicationRemovalHandler struct {
	removals RemovedApplications
}

func NewApplicationRemovalHandler(removals RemovedApplications) *ApplicationRemovalHandler {
	return &ApplicationRemovalHandler{removals: removals}
}

func (h *ApplicationRemovalHandler) Listening() []integrationmessage.Kind {
	return []integrationmessage.Kind{integrationmessage.KindOf(&accountintegrationv1.ApplicationRemovedV1{})}
}

// Handle queues the close of each unclosed Work of the Application; a failure
// is retried under the consumer's retry policy.
func (h *ApplicationRemovalHandler) Handle(ctx context.Context, msg integrationmessage.Message) error {
	fact, ok := msg.Payload().(*accountintegrationv1.ApplicationRemovedV1)
	if !ok {
		return fmt.Errorf("agent application removal: unexpected payload %T", msg.Payload())
	}
	return h.removals.ApplicationRemoved(ctx, fact.GetApplicationId())
}
