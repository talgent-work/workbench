package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type GitHubAppAuthorizationRevokedInput struct {
	ProviderAccountID string
}

func (a *PersonalConnections) HandleGitHubAppAuthorizationRevoked(ctx context.Context, input GitHubAppAuthorizationRevokedInput) error {
	accountID := strings.TrimSpace(input.ProviderAccountID)
	if accountID == "" {
		return domain.ErrInvalidRequiredField
	}
	now := time.Now().UTC()
	for after := ""; ; {
		page, err := a.authorizationEvents.AuthorizationIDsByAccount(ctx, domain.ProviderGitHub, accountID, after)
		if err != nil {
			return err
		}
		for _, id := range page.IDs {
			auth, err := a.authorizations.Get(ctx, id)
			if errors.Is(err, domain.ErrProviderNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			// Reauthorization can change the external account after selection. Never
			// apply an old account's revocation event to its replacement.
			if auth.Provider != domain.ProviderGitHub || auth.ProviderAccountID != accountID || auth.Status == domain.ProviderAuthorizationRevoked {
				continue
			}
			if err := auth.MarkRevoked(now); err != nil {
				return err
			}
			if err := a.authorizations.Save(ctx, auth); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		after = page.NextCursor
	}
}

// GitHubInstallationChangedInput reports that a GitHub App installation was
// created, removed, suspended or had its repositories or permissions changed.
type GitHubInstallationChangedInput struct {
	InstallationID  string
	SenderAccountID string
}

// HandleGitHubInstallationChanged schedules an observation of every
// authorization whose last observed scope reached the installation, and of the
// acting user's own. It changes no authorization state: an installation only
// narrows or widens reach.
func (a *PersonalConnections) HandleGitHubInstallationChanged(ctx context.Context, input GitHubInstallationChangedInput) error {
	installationID := strings.TrimSpace(input.InstallationID)
	if installationID == "" {
		return domain.ErrInvalidRequiredField
	}
	if a.observations == nil {
		return ErrProviderUnavailable
	}
	scheduled := map[string]bool{}
	schedule := func(page func(after string) (query.MaintenanceIdentities, error)) error {
		for after := ""; ; {
			identities, err := page(after)
			if err != nil {
				return err
			}
			for _, id := range identities.IDs {
				if scheduled[id] {
					continue
				}
				scheduled[id] = true
				if err := a.observations.ScheduleObservation(ctx, id); err != nil {
					return err
				}
			}
			if identities.NextCursor == "" {
				return nil
			}
			after = identities.NextCursor
		}
	}
	if err := schedule(func(after string) (query.MaintenanceIdentities, error) {
		return a.authorizationEvents.AuthorizationIDsByInstallation(ctx, domain.ProviderGitHub, installationID, after)
	}); err != nil {
		return err
	}
	if sender := strings.TrimSpace(input.SenderAccountID); sender != "" {
		return schedule(func(after string) (query.MaintenanceIdentities, error) {
			return a.authorizationEvents.AuthorizationIDsByAccount(ctx, domain.ProviderGitHub, sender, after)
		})
	}
	return nil
}
