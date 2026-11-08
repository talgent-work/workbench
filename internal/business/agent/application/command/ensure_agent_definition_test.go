package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Faults are controlled at the repository boundary. Real unique constraints and
// concurrent creation are covered by the module's database integration tests.
type definitionFaultStore struct {
	query.AgentDefinitionQueryRepository
	value                    *domain.AgentDefinition
	findErr, getErr, saveErr error
	beforeSave               func()
	lookups, saves           int
}

func (s *definitionFaultStore) FindForPurpose(_ context.Context, q query.GetAgentDefinitionForPurpose) (string, error) {
	s.lookups++
	if s.findErr != nil {
		return "", s.findErr
	}
	if s.value == nil || s.value.UsageScope.Kind != q.ScopeKind || s.value.UsageScope.Reference != q.ScopeReference || s.value.Purpose != q.Purpose {
		return "", domain.ErrAgentDefinitionNotFound
	}
	return s.value.ID, nil
}
func (s *definitionFaultStore) Get(context.Context, string) (*domain.AgentDefinition, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	value := *s.value
	return &value, nil
}
func (s *definitionFaultStore) Save(_ context.Context, value *domain.AgentDefinition) error {
	s.saves++
	if s.beforeSave != nil {
		s.beforeSave()
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	copy := *value
	copy.Version++
	s.value = &copy
	return nil
}

func employeeDefinition(t *testing.T) *domain.AgentDefinition {
	t.Helper()
	value, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"jack"},
		AgentName: "Old name's Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "jack"},
		HomeBinding:  domain.HomeBinding{OwnerID: "custom-home", Namespace: "personal"},
		Capabilities: domain.CapabilityConfiguration{Prompt: "Preserve the configured task"},
	})
	require.NoError(t, err)
	value.Version = 1
	return value
}

// changeDefinition either locates the definition again or changes its
// appearance; both write only what is unset or chosen.
func changeDefinition(ctx context.Context, store *definitionFaultStore, operation string) error {
	if operation == "ensure" {
		_, err := command.NewEnsureAgentDefinitionHandler(store, store).Handle(ctx, command.EnsureAgentDefinition{AgentDefinitionInput: domain.AgentDefinitionInput{Owners: []string{"jack"}, UsageScope: domain.UsageScope{Kind: "employee", Reference: "jack"}, Purpose: "work", HomeBinding: domain.HomeBinding{OwnerID: "caller-home", Namespace: "caller"}}, EmployeeDisplayName: "Jack"})
		return err
	}
	_, err := command.NewChangeAgentAppearanceHandler(store).Handle(ctx, command.ChangeAgentAppearance{AgentID: store.value.ID, Actor: "jack", Name: "Scout", Glyph: "rocket", Color: "violet"})
	return err
}

func expectedAppearance(operation string) domain.AgentAppearance {
	if operation == "ensure" {
		return domain.AgentAppearance{Name: "Old name's Agent", Icon: domain.DefaultAgentIcon}
	}
	return domain.AgentAppearance{Name: "Scout", Icon: domain.AgentIcon{Glyph: "rocket", Color: "violet"}}
}

func TestDefinitionNameRetryReloadsConcurrentConfiguration(t *testing.T) {
	for _, operation := range []string{"appearance"} {
		t.Run(operation, func(t *testing.T) {
			store := &definitionFaultStore{value: employeeDefinition(t)}
			originalID := store.value.ID
			store.beforeSave = func() {
				if store.saves == 1 {
					// Another writer changed capabilities after our read. Reject our stale
					// update; the name retry must reload and preserve the winning settings.
					store.value.Capabilities.Prompt = "Updated by another writer"
					store.value.Version++
					store.saveErr = domain.ErrConcurrentModification
				} else {
					store.saveErr = nil
				}
			}
			require.NoError(t, changeDefinition(t.Context(), store, operation))
			require.Equal(t, originalID, store.value.ID)
			require.Equal(t, expectedAppearance(operation), store.value.Appearance())
			require.Equal(t, "Updated by another writer", store.value.Capabilities.Prompt)
			require.Equal(t, "custom-home", store.value.HomeBinding.OwnerID)
			require.Equal(t, 2, store.saves)
			require.NoError(t, changeDefinition(t.Context(), store, operation))
			require.Equal(t, 2, store.saves, "an unchanged appearance must not write another version")
		})
	}
}

func TestDefinitionNameFailuresDoNotReportSuccessOrPersistName(t *testing.T) {
	unavailable := errors.New("storage unavailable")
	for _, operation := range []string{"ensure", "appearance"} {
		for _, fault := range []string{"lookup", "read", "write", "conflicts", "cancelled"} {
			// Locating a named definition writes nothing, so only an
			// appearance change meets write faults.
			if operation == "appearance" && fault == "lookup" || operation == "ensure" && (fault == "write" || fault == "conflicts") {
				continue
			}
			t.Run(operation+"/"+fault, func(t *testing.T) {
				store := &definitionFaultStore{value: employeeDefinition(t)}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				expected := unavailable
				switch fault {
				case "lookup":
					store.findErr = unavailable
				case "read":
					store.getErr = unavailable
				case "write":
					store.saveErr = unavailable
				case "conflicts":
					store.saveErr, expected = domain.ErrConcurrentModification, domain.ErrConcurrentModification
				case "cancelled":
					cancel()
					expected = context.Canceled
				}
				require.ErrorIs(t, changeDefinition(ctx, store, operation), expected)
				require.Equal(t, "Old name's Agent", store.value.AgentName)
				require.Equal(t, 1, store.value.Version)
				switch fault {
				case "conflicts":
					require.Equal(t, 8, store.saves, "persistent conflicts must terminate")
				case "write":
					require.Equal(t, 1, store.saves, "non-conflict storage errors must not spin")
				default:
					require.Zero(t, store.saves)
				}
				if fault == "cancelled" {
					require.Zero(t, store.lookups)
				}
			})
		}
	}
}

func TestDefinitionCreationRetryUsesWinningProjectIdentity(t *testing.T) {
	winner, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"},
		AgentName: "Existing Coordinator", Purpose: "project_coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"},
		HomeBinding: domain.HomeBinding{OwnerID: "existing-home", Namespace: "coordination"},
	})
	require.NoError(t, err)
	winner.Version = 1
	store := &definitionFaultStore{}
	store.beforeSave = func() { store.value = winner; store.saveErr = domain.ErrConcurrentModification }
	result, err := command.NewEnsureAgentDefinitionHandler(store, store).Handle(t.Context(), command.EnsureAgentDefinition{AgentDefinitionInput: domain.AgentDefinitionInput{Owners: []string{"owner"}, UsageScope: winner.UsageScope, Purpose: winner.Purpose, HomeBinding: domain.HomeBinding{OwnerID: "caller-home", Namespace: "caller"}}})
	require.NoError(t, err)
	require.Equal(t, winner.ID, result.AgentID)
	require.Equal(t, "Existing Coordinator", result.Appearance.Name, "a losing initializer must not return its generated name")
	require.Equal(t, 1, store.saves, "the winning named definition needs no repair")
	require.Equal(t, "existing-home", store.value.HomeBinding.OwnerID)
}

func TestDefinitionInitializationRejectsInvalidScopeBeforeStorage(t *testing.T) {
	store := &definitionFaultStore{}
	_, err := command.NewEnsureAgentDefinitionHandler(store, store).Handle(t.Context(), command.EnsureAgentDefinition{
		AgentDefinitionInput: domain.AgentDefinitionInput{Owners: []string{"owner"}, UsageScope: domain.UsageScope{Kind: "employee", Reference: " "}, Purpose: "work"}, EmployeeDisplayName: "Jack",
	})
	require.ErrorIs(t, err, domain.ErrInvalidAgentDefinition)
	require.Zero(t, store.lookups)
	require.Zero(t, store.saves)
}

func TestDefinitionInitializationAppliesCallerConfigurationForEveryPurpose(t *testing.T) {
	for _, purpose := range []string{"work", "project_coordination", "caller-defined"} {
		t.Run(purpose, func(t *testing.T) {
			store := &definitionFaultStore{}
			configuration := domain.AgentDefinitionInput{Owners: []string{"owner"},
				AgentName: "Caller supplied name", UsageScope: domain.UsageScope{Kind: "project", Reference: "project"}, Purpose: purpose,
				HomeBinding:  domain.HomeBinding{OwnerID: "supplied-home", Namespace: "supplied-space"},
				Capabilities: domain.CapabilityConfiguration{StartupAgent: "custom:reviewer", RequiredPlugins: []string{"custom"}},
			}
			result, err := command.NewEnsureAgentDefinitionHandler(store, store).Handle(t.Context(), command.EnsureAgentDefinition{AgentDefinitionInput: configuration})
			require.NoError(t, err)
			require.Equal(t, store.value.ID, result.AgentID)
			require.Equal(t, configuration.AgentName, result.Appearance.Name)
			require.Equal(t, domain.DefaultAgentIcon, result.Appearance.Icon)
			require.Equal(t, configuration.HomeBinding, store.value.HomeBinding)
			require.Equal(t, configuration.Capabilities, store.value.Capabilities)
		})
	}
}

// Intent (real): locating an employee's definition again, as every Work start
// does, keeps the appearance its human chose; only the human may change it.
func TestEnsureKeepsAChosenAppearanceAndOnlyTheHumanChangesIt(t *testing.T) {
	store := &definitionFaultStore{value: employeeDefinition(t)}
	_, err := command.NewChangeAgentAppearanceHandler(store).Handle(t.Context(), command.ChangeAgentAppearance{AgentID: store.value.ID, Actor: "someone-else", Name: "Imposter", Glyph: "cat", Color: "rose"})
	require.ErrorIs(t, err, domain.ErrAgentAppearanceForbidden)
	_, err = command.NewChangeAgentAppearanceHandler(store).Handle(t.Context(), command.ChangeAgentAppearance{AgentID: store.value.ID, Actor: "jack", Name: "Scout", Glyph: "unicorn", Color: "rose"})
	require.ErrorIs(t, err, domain.ErrInvalidAgentAppearance)
	require.Zero(t, store.saves)

	require.NoError(t, changeDefinition(t.Context(), store, "appearance"))
	saves := store.saves
	result, err := command.NewEnsureAgentDefinitionHandler(store, store).Handle(t.Context(), command.EnsureAgentDefinition{AgentDefinitionInput: domain.AgentDefinitionInput{Owners: []string{"jack"}, UsageScope: domain.UsageScope{Kind: "employee", Reference: "jack"}, Purpose: "work", HomeBinding: domain.HomeBinding{OwnerID: "jack", Namespace: "personal"}}, EmployeeDisplayName: "Renamed On GitHub"})
	require.NoError(t, err)
	require.Equal(t, expectedAppearance("appearance"), result.Appearance)
	require.Equal(t, expectedAppearance("appearance"), store.value.Appearance())
	require.Equal(t, saves, store.saves, "locating a named definition writes nothing")
}

// Intent (real): a new employee definition is named after its human in
// English and starts with the default icon.
func TestEnsureNamesANewEmployeeDefinitionAfterItsHuman(t *testing.T) {
	store := &definitionFaultStore{}
	result, err := command.NewEnsureAgentDefinitionHandler(store, store).Handle(t.Context(), command.EnsureAgentDefinition{AgentDefinitionInput: domain.AgentDefinitionInput{Owners: []string{"jack"}, UsageScope: domain.UsageScope{Kind: "employee", Reference: "jack"}, Purpose: "work", HomeBinding: domain.HomeBinding{OwnerID: "jack", Namespace: "personal"}}, EmployeeDisplayName: "Jack"})
	require.NoError(t, err)
	require.Equal(t, domain.AgentAppearance{Name: "Jack's Agent", Icon: domain.DefaultAgentIcon}, result.Appearance)
}
