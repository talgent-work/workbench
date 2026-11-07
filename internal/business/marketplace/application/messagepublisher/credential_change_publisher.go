// Package messagepublisher publishes Marketplace's Integration Messages.
package messagepublisher

import (
	"context"
	"log/slog"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketplaceintegrationv1 "github.com/talgent-work/contracts/gen/marketplace/integration/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

const publishTimeout = 5 * time.Second

// CredentialChangePublisher is `Publish credential change`: it publishes the
// Credential Change Published Fact for each recorded change, best effort and
// keyed by authorization so one authorization's changes stay in order.
type CredentialChangePublisher struct {
	bus    integrationmessage.Publisher
	logger *slog.Logger
}

func NewCredentialChangePublisher(bus integrationmessage.Publisher, logger *slog.Logger) *CredentialChangePublisher {
	return &CredentialChangePublisher{bus: bus, logger: logger.With("component", "marketplace.credential_change_publisher")}
}

func (p *CredentialChangePublisher) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindCredentialChanged}
}

func (p *CredentialChangePublisher) Handle(parent context.Context, event domainevent.Event) {
	changed, ok := event.(domain.CredentialChanged)
	if !ok {
		return
	}
	change, known := changes[changed.Change]
	if !known {
		p.logger.WarnContext(parent, "unknown credential change not published", slog.String("change", string(changed.Change)))
		return
	}
	payload := &marketplaceintegrationv1.CredentialChangedV1{
		EventId: uuid.Must(uuid.NewV7()).String(), OccurredAt: timestamppb.New(changed.OccurredAt),
		AuthorizationId: changed.AuthorizationID, Change: change,
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), publishTimeout)
	defer cancel()
	attrs := []any{slog.String("authorization_id", changed.AuthorizationID), slog.String("change", string(changed.Change))}
	msg, err := integrationmessage.New(integrationmessage.KindOf(payload), payload, integrationmessage.WithKey(changed.AuthorizationID))
	if err == nil {
		err = p.bus.Publish(ctx, msg)
	}
	if err != nil {
		p.logger.WarnContext(ctx, "publish credential change failed", append(attrs, slog.Any("error", err))...)
		return
	}
	p.logger.InfoContext(ctx, "credential change published", attrs...)
}

var changes = map[domain.CredentialChange]marketplaceintegrationv1.CredentialChange{
	domain.CredentialRenewed:           marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_RENEWED,
	domain.CredentialReauthorized:      marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_REAUTHORIZED,
	domain.CredentialRevoked:           marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_REVOKED,
	domain.CredentialReconnectRequired: marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_RECONNECT_REQUIRED,
}
