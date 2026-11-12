package infrastructure

import (
	"context"

	"github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"github.com/samber/oops"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountintegrationv1 "github.com/talgent-work/contracts/gen/go/account/integration/v1"
	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// AccountPublisher publishes Account's Published Fact Contracts as
// Integration Messages through the deployment's publisher.
type AccountPublisher struct {
	publisher message.Publisher
}

var _ application.AccountPublisherPort = (*AccountPublisher)(nil)

func NewAccountPublisher(publisher message.Publisher) *AccountPublisher {
	return &AccountPublisher{publisher: publisher}
}

// PublishApplicationRemoved publishes Application Removed keyed by the
// Application.
func (p *AccountPublisher) PublishApplicationRemoved(ctx context.Context, removed domain.ApplicationRemovedEvent) error {
	id := string(removed.ApplicationID)
	payload := &accountintegrationv1.ApplicationRemovedV1{
		EventId: uuid.Must(uuid.NewV7()).String(), OccurredAt: timestamppb.New(removed.OccurredAt), ApplicationId: id,
	}
	msg, err := message.New(message.KindOf(payload), payload, message.WithKey(id), message.WithOccurredAt(removed.OccurredAt))
	if err != nil {
		return oops.With("operation", "application_removed.encode").Wrap(err)
	}
	return oops.With("operation", "application_removed.publish").With("application_id", id).Wrap(p.publisher.Publish(ctx, msg))
}
