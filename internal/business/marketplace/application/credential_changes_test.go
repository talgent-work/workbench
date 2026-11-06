package application

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type recordedEvents struct{ events []domainevent.Event }

func (r *recordedEvents) Dispatch(_ context.Context, event domainevent.Event) error {
	r.events = append(r.events, event)
	return nil
}
func (r *recordedEvents) DispatchAll(_ context.Context, events []domainevent.Event) error {
	r.events = append(r.events, events...)
	return nil
}
func (*recordedEvents) Close(context.Context) error { return nil }

type failingSave struct{ AuthorizationStore }

func (failingSave) Save(context.Context, *domain.ProviderAuthorization) error {
	return errors.New("mysql unavailable")
}

// A credential change reaches consumers only once the authorization holding
// it is saved, and only once.
func TestCredentialChangesAreDispatchedOnceSaved(t *testing.T) {
	ctx := context.Background()
	events := &recordedEvents{}
	repository := &fakeAuthorizationRepository{authorizations: map[string]*domain.ProviderAuthorization{}}
	store := NewCredentialChangeDispatch(repository, events, slog.Default())
	auth := mustAuthorization(t, "owner", "octocat")
	require.NoError(t, store.Save(ctx, auth))
	require.Empty(t, events.events, "saving without a change dispatches nothing")

	require.NoError(t, auth.MarkRevoked(time.Now()))
	require.Error(t, NewCredentialChangeDispatch(failingSave{repository}, events, slog.Default()).Save(ctx, auth))
	require.Empty(t, events.events, "an unsaved change is not published")
	require.NoError(t, store.Save(ctx, auth))
	require.Len(t, events.events, 1)
	changed := events.events[0].(domain.CredentialChanged)
	require.Equal(t, auth.ID, changed.AuthorizationID)
	require.Equal(t, domain.CredentialRevoked, changed.Change)
	require.NoError(t, store.Save(ctx, auth))
	require.Len(t, events.events, 1, "a saved change is dispatched once")
}
