package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var (
	ErrRepositoryAuthorizationUnavailable = errors.New("agent: selected repository authorization is unavailable")
	// ErrRepositoryAccessChanged means the Work's repository access changed
	// while its credentials were supplied; a retry supplies the current one.
	ErrRepositoryAccessChanged = errors.New("agent: repository access changed while it was supplied")
)

type RepositoryAccessReference struct {
	AccessID, RepositoryID, CloneURL, AuthorizationID string
}

type RepositoryAccessMaterial struct {
	RepositoryAccessReference
	command.RepositoryCredentialMaterial
}

// RepositoryAccessFailure is a repository no credential was supplied for;
// Temporary marks a failure that confirmed nothing about the authorization.
type RepositoryAccessFailure struct {
	RepositoryID, Reason string
	Temporary            bool
}
type RepositoryAccessResult struct {
	Credentials []RepositoryAccessMaterial
	Unavailable []RepositoryAccessFailure
}

// RepositoryAccess supplies the temporary access the Work's current start
// takes to its selected repositories, from the Work's retained selection and
// Marketplace's current credential supply, at the time it is delivered. It
// owns no grant lifecycle.
type RepositoryAccess struct {
	works  domain.AgentWorkRepository
	supply command.MarketplacePort
}

func NewRepositoryAccess(works domain.AgentWorkRepository, supply command.MarketplacePort) *RepositoryAccess {
	return &RepositoryAccess{works: works, supply: supply}
}

// Plan names the access each selected repository of the Work takes, for its
// owner, while the Work's current start holds an active process: none once
// the Work entered Closing (Repository preparation cancellation).
func (a *RepositoryAccess) Plan(ctx context.Context, owner, workID string) ([]RepositoryAccessReference, error) {
	work, err := a.works.Get(ctx, workID)
	if err != nil {
		return nil, err
	}
	if owner == "" || owner != work.RepresentedPerson {
		return nil, access.ErrAccessDenied
	}
	if err := work.AdmitRepositoryAccess(owner); err != nil {
		if errors.Is(err, domain.ErrRepositoryPreparationCanceled) || errors.Is(err, domain.ErrRepositoryPreparationConflict) {
			return nil, errors.Join(access.ErrAccessDenied, err)
		}
		return nil, err
	}
	var refs []RepositoryAccessReference
	for _, checkout := range command.PlanCheckouts(work.ID, work.RepositorySelection) {
		ref, err := work.RepositoryAuthorization(checkout.RepoID)
		if err != nil && !errors.Is(err, domain.ErrServiceAuthorizationNotSelected) {
			return nil, errors.Join(ErrRepositoryAuthorizationUnavailable, err)
		}
		refs = append(refs, RepositoryAccessReference{AccessID: checkout.AccessID,
			RepositoryID: checkout.RepoID, CloneURL: checkout.CloneURL, AuthorizationID: ref.AuthorizationID})
	}
	return refs, nil
}

func (a *RepositoryAccess) Resolve(ctx context.Context, owner, workID string) (RepositoryAccessResult, error) {
	refs, err := a.Plan(ctx, owner, workID)
	if err != nil {
		return RepositoryAccessResult{}, err
	}
	result := RepositoryAccessResult{}
	for _, ref := range refs {
		if ref.AuthorizationID == "" {
			result.Unavailable = append(result.Unavailable, RepositoryAccessFailure{RepositoryID: ref.RepositoryID, Reason: "not_selected"})
			continue
		}
		material, err := a.supply.SupplyRepositoryCredential(ctx, command.RepositoryCredentialRequest{OwnerUserID: owner, AuthorizationID: ref.AuthorizationID, RepositoryID: ref.RepositoryID, CloneURL: ref.CloneURL})
		if err != nil {
			if ctx.Err() != nil {
				return RepositoryAccessResult{}, ctx.Err()
			}
			reason, temporary := refusal(err)
			result.Unavailable = append(result.Unavailable, RepositoryAccessFailure{RepositoryID: ref.RepositoryID, Reason: reason, Temporary: temporary})
			continue
		}
		if !usableRepositoryCredential(material) {
			result.Unavailable = append(result.Unavailable, RepositoryAccessFailure{RepositoryID: ref.RepositoryID, Reason: "unavailable", Temporary: true})
			continue
		}
		result.Credentials = append(result.Credentials, RepositoryAccessMaterial{RepositoryAccessReference: ref, RepositoryCredentialMaterial: material})
	}
	current, err := a.Plan(ctx, owner, workID)
	if err != nil {
		return RepositoryAccessResult{}, err
	}
	if !slices.Equal(refs, current) {
		return RepositoryAccessResult{}, ErrRepositoryAccessChanged
	}
	return result, nil
}

func usableRepositoryCredential(material command.RepositoryCredentialMaterial) bool {
	return material.Username != "" && !strings.Contains(material.Username, ":") && material.Password != "" && !credentialExpired(material.ExpiresAt, time.Now())
}

// credentialExpired reports whether the expiry Marketplace declared has
// passed; a credential that declares none does not expire.
func credentialExpired(expiresAt, now time.Time) bool {
	return !expiresAt.IsZero() && !expiresAt.After(now)
}
