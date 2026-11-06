package domain

import (
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
)

// CredentialChange names how the credential of a Provider Authorization
// changed for its consumers.
type CredentialChange string

const (
	CredentialRenewed           CredentialChange = "renewed"
	CredentialReauthorized      CredentialChange = "reauthorized"
	CredentialRevoked           CredentialChange = "revoked"
	CredentialReconnectRequired CredentialChange = "reconnect_required"
)

const EventKindCredentialChanged domainevent.Kind = "marketplace.credential.changed"

// CredentialChanged records that consumers of the authorization must obtain
// its current credentials again. It carries no credential material.
type CredentialChanged struct {
	AuthorizationID string
	Change          CredentialChange
	OccurredAt      time.Time
}

func (CredentialChanged) Kind() domainevent.Kind { return EventKindCredentialChanged }
func (e CredentialChanged) Key() string          { return e.AuthorizationID }

// Events drains the domain events the authorization recorded since it was
// loaded; the caller dispatches them once the authorization is saved.
func (a *ProviderAuthorization) Events() []domainevent.Event {
	if a.events == nil {
		return nil
	}
	events := a.events.Drain()
	a.events = nil
	return events
}

func (a *ProviderAuthorization) recordCredentialChange(change CredentialChange, now time.Time) {
	if a.events == nil {
		a.events = domainevent.NewCollection()
	}
	a.events.Add(CredentialChanged{AuthorizationID: a.ID, Change: change, OccurredAt: now.UTC()})
}
