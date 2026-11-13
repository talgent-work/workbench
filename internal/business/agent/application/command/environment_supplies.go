package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// supplyAttemptTimeout bounds what one short step asks of other authorities,
// so an unresponsive Storage or Marketplace never holds up a reconciliation
// for long.
const supplyAttemptTimeout = 5 * time.Second

// RepositorySupply supplies the temporary access a Work's current start takes
// to the repositories it selected (RepositoryAccess).
type RepositorySupply interface {
	// Plan names the access each selected repository takes, for the Work's
	// represented person.
	Plan(ctx context.Context, owner, workID string) ([]RepositoryAccessReference, error)
	// Resolve obtains each planned repository's current credential from
	// Marketplace.
	Resolve(ctx context.Context, owner, workID string) (RepositoryAccessResult, error)
}

// ServiceSupply supplies the current credentials of the service targets a
// Work's run may reach (ServiceAccess).
type ServiceSupply interface {
	Resolve(ctx context.Context, owner, workID string, requirements []ServiceRequirement) (ServiceAccessResult, error)
}

// EnvironmentSupplies obtains, from the authorities Agent does not own, what
// the environment steps of an execution hand its environment: Storage's
// Mount Grants, Space Token and input files, and Marketplace's current
// repository and service credentials. The environment adapter receives them
// and calls neither authority itself.
type EnvironmentSupplies struct {
	repositories RepositorySupply
	services     ServiceSupply
	storage      StoragePort
	accounts     AccountPort
}

func NewEnvironmentSupplies(repositories RepositorySupply, services ServiceSupply, storage StoragePort, accounts AccountPort) *EnvironmentSupplies {
	return &EnvironmentSupplies{repositories: repositories, services: services, storage: storage, accounts: accounts}
}

// creation is what a Cloud execution's specification fixes: the Mount Grants
// of exactly the Work's located layout and the access its selected
// repositories take. A Work with no authorization owner selects no
// repository; a Managed execution takes nothing.
func (s *EnvironmentSupplies) creation(ctx context.Context, execution *domain.AgentExecution) (CreationSupply, error) {
	if execution.Realization != domain.RealizationCloud {
		return CreationSupply{}, nil
	}
	mounts, err := s.storage.AcquireMounts(ctx, execution.WorkID, execution.Launch.LocatedLayout.MountRequests())
	if err != nil {
		return CreationSupply{}, err
	}
	supply := CreationSupply{Mounts: mounts}
	if owner := execution.Launch.AuthorizationOwner(); owner != "" {
		if supply.Repositories, err = s.repositories.Plan(ctx, owner, execution.WorkID); err != nil {
			return CreationSupply{}, err
		}
	}
	return supply, nil
}

// credentials is what a delivery hands the bound environment now: the Work's
// Identity Token, which Account issues afresh for every delivery (Work token
// issuance), the Space Token of the Work's publication target, the same on
// every opening, and on a Cloud the current credentials of the repositories
// and service targets the run may reach, with the access observed for each.
// A Work with no authorization owner holds no Provider Authorization,
// so none of its targets is supplied.
func (s *EnvironmentSupplies) credentials(ctx context.Context, execution *domain.AgentExecution) (CredentialSupply, error) {
	ctx, cancel := context.WithTimeout(ctx, supplyAttemptTimeout)
	defer cancel()
	identity, err := s.identityToken(ctx, execution.Launch)
	if err != nil {
		return CredentialSupply{}, err
	}
	token, err := s.storage.OpenDirectory(ctx, execution.Launch.LocatedLayout.PublicationTarget)
	if err != nil {
		return CredentialSupply{}, err
	}
	supply := CredentialSupply{IdentityToken: identity, SpaceToken: token}
	if execution.Realization != domain.RealizationCloud {
		return supply, nil
	}
	launch := execution.Launch
	required := requiredServices(launch.Capabilities, launch.AuthorizationSelection)
	services, repositories := required.unselected(), RepositoryAccessResult{}
	if owner := launch.AuthorizationOwner(); owner != "" {
		if repositories, err = s.repositories.Resolve(ctx, owner, execution.WorkID); err != nil {
			return CredentialSupply{}, err
		}
		if services, err = s.services.Resolve(ctx, owner, execution.WorkID, required.Targets); err != nil {
			return CredentialSupply{}, err
		}
	}
	supply.Repositories, supply.Services = repositories.Credentials, services.Credentials
	supply.Access = append(required.statuses(services), repositoryStatuses(repositories)...)
	return supply, nil
}

// identityToken asks Account for the Work's agent Identity Token, stating the
// Work's fixed facts as its start fixed them: its Work Caller, Work Key and
// File Home, AgentDefinition and its purpose, the person its Agent
// represents, the Application that owns it and its Work Labels.
func (s *EnvironmentSupplies) identityToken(ctx context.Context, launch domain.LaunchSpecification) (IdentityToken, error) {
	return s.accounts.IssueWorkIdentityToken(ctx, WorkIdentityFacts{
		WorkID:            launch.WorkID,
		WorkCaller:        launch.WorkCaller,
		WorkKey:           launch.WorkKey,
		FileHome:          launch.FileHome,
		AgentID:           launch.AgentID,
		Purpose:           launch.Purpose,
		RepresentedPerson: launch.RepresentedPerson,
		OwningApplication: launch.OwningApplication,
		WorkLabels:        launch.WorkLabels,
	})
}

// inputs lists the located inputs' files, each with a fresh download Access
// Link; a file deleted since it was listed is left out, and its change fact
// brings the next sync.
func (s *EnvironmentSupplies) inputs(ctx context.Context, located *domain.LocatedDirectory) (ManagedInputs, error) {
	if located == nil {
		return ManagedInputs{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, supplyAttemptTimeout)
	defer cancel()
	files, err := s.storage.ListFiles(ctx, located.Directory)
	if err != nil {
		return ManagedInputs{}, err
	}
	inputs := ManagedInputs{Located: true, Files: make([]InputFile, 0, len(files))}
	for _, file := range files {
		link, err := s.storage.IssueAccessLink(ctx, FilePosition{Directory: located.Directory, Name: file.Name}, false)
		if errors.Is(err, ErrFileNotFound) {
			continue
		}
		if err != nil {
			return ManagedInputs{}, err
		}
		inputs.Files = append(inputs.Files, InputFile{Name: file.Name, SizeBytes: file.SizeBytes, UpdatedAt: file.UpdatedAt, AccessPath: link.Path})
	}
	return inputs, nil
}
