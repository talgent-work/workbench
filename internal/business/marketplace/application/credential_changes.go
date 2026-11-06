package application

import (
	"context"
	"log/slog"

	domainevent "github.com/go-jimu/components/ddd/event"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// CredentialChangeDispatch is the AuthorizationStore every credential
// operation saves through. Once an authorization is saved it dispatches the
// credential changes the authorization recorded, so renewal, reauthorization,
// cancellation and accepted revocation all reach consumers however they were
// triggered. Publication is best effort.
type CredentialChangeDispatch struct {
	AuthorizationStore
	events domainevent.Dispatcher
	logger *slog.Logger
}

var _ AuthorizationStore = (*CredentialChangeDispatch)(nil)

func NewCredentialChangeDispatch(store AuthorizationStore, events domainevent.Dispatcher, logger *slog.Logger) *CredentialChangeDispatch {
	return &CredentialChangeDispatch{AuthorizationStore: store, events: events, logger: logger}
}

func (s *CredentialChangeDispatch) Save(ctx context.Context, authorization *domain.ProviderAuthorization) error {
	if err := s.AuthorizationStore.Save(ctx, authorization); err != nil {
		return err
	}
	if events := authorization.Events(); len(events) > 0 {
		if err := s.events.DispatchAll(ctx, events); err != nil {
			s.logger.WarnContext(ctx, "dispatch credential changes failed", slog.String("authorization_id", authorization.ID), slog.Any("error", err))
		}
	}
	return nil
}
