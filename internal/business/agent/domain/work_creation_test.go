package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

var projectA = domain.WorkCaller{Kind: "project", ID: "project-a"}

func personDefinition(t *testing.T, person string, public bool) *domain.AgentDefinition {
	t.Helper()
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{person}, Purpose: domain.StandaloneWorkPurpose,
		UsageScope:   domain.UsageScope{Kind: domain.EmployeeScopeKind, Reference: person},
		Capabilities: domain.CapabilityConfiguration{Prompt: "Base"}, WorkDefaults: domain.WorkDefaults{StartupAgent: "employees:member"},
		Visibility: domain.VisibilityRule{Public: public},
	})
	require.NoError(t, err)
	return definition
}

func creationRequest(definition *domain.AgentDefinition, caller domain.WorkCaller) domain.WorkCreationRequest {
	return domain.WorkCreationRequest{StartedBy: definition.Owners[0], WorkCaller: caller,
		Admission:    domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true},
		WorkModel:    domain.WorkModelSelection{ConfigurationID: "deployment-default"},
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Prompt: "Specific"},
	}
}

// Within one AgentDefinition a Work Key has one Work for good: a repeat
// returns it, closed or not, whatever the request's capabilities, and only a
// new key, or another definition, establishes a new Work.
func TestWorkKeyReturnsItsOneWork(t *testing.T) {
	definition := personDefinition(t, "owner", false)
	lookup := &definitionSessionLookup{}
	request := creationRequest(definition, projectA)
	request.WorkKey = domain.WorkKey{Kind: "case", ID: "case-a"}
	work, created, err := createWork(definition, request, lookup)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, projectA, work.WorkCaller)
	require.Equal(t, request.WorkKey, work.WorkKey)
	require.False(t, work.FileHome.Present())

	lookup.session = work
	request.Capabilities = domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, StartupAgent: "missing:role"}
	reused, created, err := createWork(definition, request, lookup)
	require.NoError(t, err)
	require.False(t, created, "the existing Work wins before new capabilities are validated")
	require.Same(t, work, reused)
	require.Equal(t, "Specific", reused.CapabilitySnapshot.Prompt)

	work.State = domain.WorkClosed
	closed, created, err := createWork(definition, request, lookup)
	require.NoError(t, err)
	require.False(t, created, "a closed Work still holds its Work Key")
	require.Same(t, work, closed)

	request.WorkKey = domain.WorkKey{Kind: "case", ID: "case-b"}
	other, created, err := createWork(definition, request, lookup)
	require.NoError(t, err)
	require.True(t, created, "a Work of another Work Key is never reused")
	require.NotEqual(t, work.ID, other.ID)

	request.WorkKey = work.WorkKey
	lookup.session = &domain.AgentWork{ID: "other-definition", AgentID: "other", WorkKey: work.WorkKey, WorkCaller: projectA}
	_, created, err = createWork(definition, request, lookup)
	require.NoError(t, err)
	require.True(t, created, "nor one of another definition")
}

// A Work Key's Work of another Work Caller or File Home refuses the creation
// rather than answering for a different party or shared space.
func TestMismatchedReuseIsRefused(t *testing.T) {
	definition := personDefinition(t, "owner", false)
	request := creationRequest(definition, projectA)
	request.WorkKey, request.FileHome = domain.WorkKey{Kind: "case", ID: "case-a"}, domain.FileHome{Kind: "case", ID: "case-a"}
	work, _, err := createWork(definition, request, &definitionSessionLookup{})
	require.NoError(t, err)
	lookup := &definitionSessionLookup{session: work}

	otherCaller := request
	otherCaller.WorkCaller = domain.WorkCaller{Kind: "project", ID: "project-b"}
	_, _, err = createWork(definition, otherCaller, lookup)
	require.ErrorIs(t, err, domain.ErrMismatchedReuse)

	otherHome := request
	otherHome.FileHome = domain.FileHome{}
	_, _, err = createWork(definition, otherHome, lookup)
	require.ErrorIs(t, err, domain.ErrMismatchedReuse, "a missing File Home differs too")

	same, created, err := createWork(definition, request, lookup)
	require.NoError(t, err)
	require.False(t, created)
	require.Same(t, work, same)
}

// A creation without a Work Key always establishes its own Work, which has no
// Work Key for good.
func TestKeylessCreationEstablishesANewWork(t *testing.T) {
	definition := personDefinition(t, "owner", false)
	lookup := &definitionSessionLookup{session: &domain.AgentWork{ID: "existing", AgentID: definition.ID, WorkCaller: projectA}}
	request := creationRequest(definition, projectA)
	first, created, err := createWork(definition, request, lookup)
	require.NoError(t, err)
	require.True(t, created)
	second, created, err := createWork(definition, request, lookup)
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, first.ID, second.ID)
	require.False(t, first.WorkKey.Present())
}

// Each fact names a kind and an identity; a kind never holds the colon that
// separates the two.
func TestWorkFactsNameAKindAndAnIdentity(t *testing.T) {
	definition := personDefinition(t, "owner", false)
	for name, change := range map[string]func(*domain.WorkCreationRequest){
		"no Work Caller":                      func(r *domain.WorkCreationRequest) { r.WorkCaller = domain.WorkCaller{} },
		"a Work Caller without identity":      func(r *domain.WorkCreationRequest) { r.WorkCaller = domain.WorkCaller{Kind: "project"} },
		"a Work Key without identity":         func(r *domain.WorkCreationRequest) { r.WorkKey = domain.WorkKey{Kind: "case"} },
		"a Work Key kind with a colon":        func(r *domain.WorkCreationRequest) { r.WorkKey = domain.WorkKey{Kind: "case:a", ID: "1"} },
		"a File Home without kind":            func(r *domain.WorkCreationRequest) { r.FileHome = domain.FileHome{ID: "1"} },
		"a Work Key identity with a space":    func(r *domain.WorkCreationRequest) { r.WorkKey = domain.WorkKey{Kind: "case", ID: "a b"} },
		"a Work Caller identity with a comma": func(r *domain.WorkCreationRequest) { r.WorkCaller = domain.WorkCaller{Kind: "project", ID: "a,b"} },
	} {
		t.Run(name, func(t *testing.T) {
			request := creationRequest(definition, projectA)
			change(&request)
			_, _, err := createWork(definition, request, &definitionSessionLookup{})
			require.Error(t, err)
			require.True(t, errorsAnyOf(err, domain.ErrInvalidWorkCaller, domain.ErrInvalidWorkKey, domain.ErrInvalidFileHome))
		})
	}
}

// A Standalone Work records its owner as its Work Caller, carries no Work
// Key and no File Home, and stays private whatever its definition declares.
func TestStandaloneWorkFacts(t *testing.T) {
	definition := personDefinition(t, "owner", true)
	owner := domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "owner"}
	work, created, err := createWork(definition, creationRequest(definition, owner), &definitionSessionLookup{})
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, work.Standalone())
	require.False(t, work.Visibility.Public, "a Standalone Work is private whatever its definition declares")
	_, err = work.ChangeVisibility(definition, "owner", true, time.Now())
	require.ErrorIs(t, err, domain.ErrStandaloneWorkPrivate)

	keyed := creationRequest(definition, owner)
	keyed.WorkKey = domain.WorkKey{Kind: "case", ID: "1"}
	_, _, err = createWork(definition, keyed, &definitionSessionLookup{})
	require.ErrorIs(t, err, domain.ErrStandaloneWorkFacts)
	homed := creationRequest(definition, owner)
	homed.FileHome = domain.FileHome{Kind: "case", ID: "1"}
	_, _, err = createWork(definition, homed, &definitionSessionLookup{})
	require.ErrorIs(t, err, domain.ErrStandaloneWorkFacts)

	for _, caller := range []domain.WorkCaller{{Kind: domain.CallerKindPerson, ID: "someone-else"}, {Kind: domain.CallerKindApplication, ID: "owner"}} {
		_, _, err = createWork(definition, creationRequest(definition, caller), &definitionSessionLookup{})
		require.ErrorIs(t, err, domain.ErrWorkCallerNotOwner, "a Standalone Work's Work Caller is its owner, not %v", caller)
	}
}

// An Application's Standalone Work names the Application as its Work Caller.
func TestApplicationWorkCallerIsTheApplication(t *testing.T) {
	const application = "01a19bc3-075a-7c57-b42b-7fdf73b61100"
	definition, err := domain.NewAgentDefinition(domain.ApplicationDefinitionInput(application, "Reporter"))
	require.NoError(t, err)
	caller, ok := definition.OwnerWorkCaller()
	require.True(t, ok)
	require.Equal(t, domain.WorkCaller{Kind: domain.CallerKindApplication, ID: application}, caller)
	work, _, err := createWork(definition, creationRequest(definition, caller), &definitionSessionLookup{})
	require.NoError(t, err)
	require.True(t, work.Standalone())
	_, _, err = createWork(definition, creationRequest(definition, domain.WorkCaller{Kind: domain.CallerKindPerson, ID: application}), &definitionSessionLookup{})
	require.ErrorIs(t, err, domain.ErrWorkCallerNotOwner)
}

// An extension starts Works only under a Work Caller of a kind it defines; a
// person's or an Application's Work is the owner's own to start.
func TestExtensionStartsWorksOnlyUnderItsOwnKinds(t *testing.T) {
	require.NoError(t, domain.AdmitExtensionCreation(projectA))
	require.NoError(t, domain.AdmitExtensionCreation(domain.WorkCaller{Kind: "review", ID: "r1"}))
	for _, kind := range []string{domain.CallerKindPerson, domain.CallerKindApplication} {
		require.ErrorIs(t, domain.AdmitExtensionCreation(domain.WorkCaller{Kind: kind, ID: "x"}), domain.ErrStandaloneWorkOwnerOnly)
	}
	require.ErrorIs(t, domain.AdmitExtensionCreation(domain.WorkCaller{}), domain.ErrInvalidWorkCaller)

	pm, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "project_coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"}, Visibility: domain.VisibilityRule{Public: true}})
	require.NoError(t, err)
	_, ok := pm.OwnerWorkCaller()
	require.False(t, ok, "a definition that represents no one starts no Standalone Work")
	_, _, err = createWork(pm, creationRequest(pm, domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "alice"}), &definitionSessionLookup{})
	require.ErrorIs(t, err, domain.ErrWorkCallerNotOwner)
	request := creationRequest(pm, projectA)
	request.WorkKey = domain.WorkKey{Kind: "milestone", ID: "m1"}
	work, _, err := createWork(pm, request, &definitionSessionLookup{})
	require.NoError(t, err)
	require.False(t, work.Standalone())
	require.True(t, work.Visibility.Public, "an Extension Work takes its definition's declared visibility")
}

// Every Work's telemetry carries its Work Caller and, when it has one, its
// Work Key, which no supplied attribute replaces; the Project attribute is
// gone.
func TestWorkTelemetryCarriesWorkCallerAndWorkKey(t *testing.T) {
	definition := personDefinition(t, "owner", false)
	request := creationRequest(definition, projectA)
	request.WorkKey = domain.WorkKey{Kind: "case", ID: "case-a"}
	request.TraceAttributes = map[string]string{"talgent.case_id": "supplied", domain.TraceAttributeWorkCaller: "project:forged", domain.TraceAttributeWorkKey: "case:forged"}
	work, _, err := createWork(definition, request, &definitionSessionLookup{})
	require.NoError(t, err)
	attributes := follow(work).launch().TraceAttributes
	require.Equal(t, "project:project-a", attributes[domain.TraceAttributeWorkCaller])
	require.Equal(t, "case:case-a", attributes[domain.TraceAttributeWorkKey])
	require.Equal(t, "supplied", attributes["talgent.case_id"], "Agent carries supplied attributes unchanged")
	require.NotContains(t, attributes, "talgent.project_id")

	standalone := creationRequest(definition, domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "owner"})
	standalone.TraceAttributes = map[string]string{domain.TraceAttributeWorkKey: "case:forged"}
	own, _, err := createWork(definition, standalone, &definitionSessionLookup{})
	require.NoError(t, err)
	attributes = follow(own).launch().TraceAttributes
	require.Equal(t, "person:owner", attributes[domain.TraceAttributeWorkCaller])
	require.NotContains(t, attributes, domain.TraceAttributeWorkKey, "a Work without a Work Key carries none")
}

func errorsAnyOf(err error, targets ...error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
