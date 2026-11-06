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

var ErrRepositoryAuthorizationUnavailable = errors.New("agent: selected repository authorization is unavailable")

type RepositoryCredentialRequest struct {
	OwnerUserID, AuthorizationID, RepositoryID, CloneURL string
}

type RepositoryCredentialMaterial struct {
	Username, Password string
	ExpiresAt          time.Time
}

type RepositoryCredentialPort interface {
	SupplyRepositoryCredential(context.Context, RepositoryCredentialRequest) (RepositoryCredentialMaterial, error)
}

type RepositoryAccessReference struct {
	AccessID, RepositoryID, CloneURL, AuthorizationID string
}

type RepositoryAccessMaterial struct {
	RepositoryAccessReference
	RepositoryCredentialMaterial
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

// RepositoryAccess coordinates retained Work and preparation authority with
// Marketplace's current credential supply. It owns no grant lifecycle.
type RepositoryAccess struct {
	works    domain.AgentWorkRepository
	requests command.InitialExecutionReader
	supply   RepositoryCredentialPort
}

func NewRepositoryAccess(works domain.AgentWorkRepository, requests command.InitialExecutionReader, supply RepositoryCredentialPort) *RepositoryAccess {
	return &RepositoryAccess{works: works, requests: requests, supply: supply}
}

func (a *RepositoryAccess) Plan(ctx context.Context, owner, workID string) ([]RepositoryAccessReference, error) {
	work, err := a.works.Get(ctx, workID)
	if err != nil {
		return nil, err
	}
	if owner == "" || owner != work.HomeBinding.OwnerID {
		return nil, access.ErrAccessDenied
	}
	if err := work.AuthorizeCredentialAccess(); err != nil {
		return nil, err
	}
	request, err := a.requests.Get(ctx, workID)
	if errors.Is(err, command.ErrInitialExecutionNotFound) && len(work.RepositorySelection.Repositories) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if request.WorkID != workID || request.SubjectID != work.StartedBy || request.Rejected {
		return nil, access.ErrAccessDenied
	}
	if !request.Prepared {
		return nil, command.ErrInitialExecutionPending
	}
	if len(request.Checkouts) != len(work.RepositorySelection.Repositories) {
		return nil, command.ErrInitialExecutionConflict
	}
	if err := request.PlanCheckouts(work); err != nil {
		if errors.Is(err, domain.ErrRepositoryPreparationCanceled) {
			return nil, errors.Join(access.ErrAccessDenied, err)
		}
		return nil, err
	}
	var refs []RepositoryAccessReference
	for _, checkout := range request.Checkouts {
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
		material, err := a.supply.SupplyRepositoryCredential(ctx, RepositoryCredentialRequest{OwnerUserID: owner, AuthorizationID: ref.AuthorizationID, RepositoryID: ref.RepositoryID, CloneURL: ref.CloneURL})
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
		return RepositoryAccessResult{}, command.ErrInitialExecutionConflict
	}
	return result, nil
}

func usableRepositoryCredential(material RepositoryCredentialMaterial) bool {
	return material.Username != "" && !strings.Contains(material.Username, ":") && material.Password != "" && !credentialExpired(material.ExpiresAt, time.Now())
}

// credentialExpired reports whether the expiry Marketplace declared has
// passed; a credential that declares none does not expire.
func credentialExpired(expiresAt, now time.Time) bool {
	return !expiresAt.IsZero() && !expiresAt.After(now)
}
