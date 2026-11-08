package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func owner(facts ...string) []domain.OwnerFact {
	owner := make([]domain.OwnerFact, 0, len(facts)/2)
	for i := 0; i < len(facts); i += 2 {
		owner = append(owner, domain.OwnerFact{Condition: domain.LocatorCondition(facts[i]), Value: facts[i+1]})
	}
	return owner
}

func located(name string, backend domain.DirectoryBackend, lifetime domain.DirectoryLifetime, facts ...string) domain.LocatedDirectory {
	return domain.LocatedDirectory{Directory: domain.Directory{Owner: owner(facts...), Name: name}, Backend: backend, Lifetime: lifetime}
}

func layoutCreation(definition *domain.AgentDefinition, startedBy string, managed bool) domain.WorkCreationRequest {
	kind, model := domain.ComputerCloud, domain.WorkModelSelection{ConfigurationID: "default"}
	if managed {
		kind, model = domain.ComputerManaged, domain.MachineLoginModel()
	}
	return domain.WorkCreationRequest{StartedBy: startedBy, ComputerManaged: managed, WorkModel: model,
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}},
		Admission:    domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: kind, DeskID: "desk-test"}, ModelConfigurationProvided: true}}
}

// employeeDefinition is a person's definition without the required runtime
// Plugin, which the Works here do not select.
func employeeDefinition(t *testing.T, person string) *domain.AgentDefinition {
	t.Helper()
	input := domain.EmployeeDefinitionInput(person)
	input.Capabilities = domain.CapabilityConfiguration{}
	definition, err := domain.NewAgentDefinition(input)
	require.NoError(t, err)
	return definition
}

func projectDefinition(t *testing.T, project string) *domain.AgentDefinition {
	t.Helper()
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"pm"}, Purpose: "project_coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: project}})
	require.NoError(t, err)
	return definition
}

func mountAt(layout domain.LocatedLayout, path string) (domain.LocatedMount, bool) {
	for _, mount := range layout.Mounts {
		if mount.Path == path {
			return mount, true
		}
	}
	return domain.LocatedMount{}, false
}

// Employee and project definitions start with the product default Work
// Layout, whatever their scope.
func TestEmployeeAndProjectDefinitionsGetTheDefaultLayout(t *testing.T) {
	employee, err := domain.NewAgentDefinition(domain.EmployeeDefinitionInput("jack"))
	require.NoError(t, err)
	require.Equal(t, domain.DefaultWorkLayout(), employee.WorkLayout)
	require.Equal(t, domain.DefaultWorkLayout(), projectDefinition(t, "p1").WorkLayout)
	require.NoError(t, domain.DefaultWorkLayout().Validate())
}

// A Work Layout is checked when its definition is saved: one mount per path,
// both roots as read-write NAS Work Directories following the Work, paths
// beneath them, and a mount following the Subject read-only.
func TestAWorkLayoutIsCheckedAtSave(t *testing.T) {
	knowledge := domain.DirectoryLocator{Conditions: []domain.LocatorCondition{domain.ConditionProject}, Name: "knowledge", Backend: domain.BackendJuiceFS, Lifetime: domain.LifetimePermanent}
	cases := map[string]func(*domain.WorkLayout){
		"two mounts at one path": func(l *domain.WorkLayout) {
			l.Mounts = append(l.Mounts, domain.LayoutMount{Path: "/workspace/knowledge", ReadOnly: true, Locators: []domain.DirectoryLocator{knowledge}})
		},
		"no HOME":            func(l *domain.WorkLayout) { l.Mounts = l.Mounts[1:] },
		"read-only root":     func(l *domain.WorkLayout) { l.Mounts[1].ReadOnly = true },
		"outside the roots":  func(l *domain.WorkLayout) { l.Mounts[4].Path = "/etc/knowledge" },
		"escaping a root":    func(l *domain.WorkLayout) { l.Mounts[4].Path = "/workspace/../etc" },
		"writable Subject":   func(l *domain.WorkLayout) { l.Mounts[3].ReadOnly = false },
		"unknown fact":       func(l *domain.WorkLayout) { l.Mounts[4].Locators[0].Conditions = []domain.LocatorCondition{"team"} },
		"Work lifetime":      func(l *domain.WorkLayout) { l.Mounts[4].Locators[0].Lifetime = domain.LifetimeWork },
		"two Harness states": func(l *domain.WorkLayout) { l.Mounts = append(l.Mounts, l.Mounts[2]) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			definition := employeeDefinition(t, "jack")
			layout := domain.DefaultWorkLayout()
			change(&layout)
			definition.WorkLayout = layout
			require.ErrorIs(t, definition.Validate(), domain.ErrInvalidWorkLayout)
			require.ErrorIs(t, definition.Validate(), domain.ErrInvalidAgentDefinition)
		})
	}
}

// A Work with a Subject and a Project, started by the person its Agent
// represents, locates its own HOME and Session Workspace, its definition's
// Harness state, its Subject's inputs and its Project's knowledge, and
// publishes into its Subject's Artifacts.
func TestAWorkWithASubjectAndProjectLocatesItsDirectories(t *testing.T) {
	definition := employeeDefinition(t, "jack")
	request := layoutCreation(definition, "jack", false)
	request.StartingUser, request.Subject, request.ProjectID = "jack", domain.WorkSubject{Kind: "case", ID: "1"}, "p1"
	work, _, err := definition.CreateWork(request, domain.WorkCreationFacts{})
	require.NoError(t, err)

	require.Equal(t, domain.ClaudeCode.ID, work.AgentHarnessReference)
	require.Equal(t, []domain.LocatedMount{
		{Path: "$HOME", Directory: located("home", domain.BackendNAS, domain.LifetimeWork, "work", work.ID)},
		{Path: "/workspace", Directory: located("workspace", domain.BackendNAS, domain.LifetimeWork, "work", work.ID)},
		{Path: "$HOME/.claude", Directory: located("claude-code", domain.BackendNAS, domain.LifetimePermanent, "agent_definition", definition.ID)},
		{Path: "/workspace/inputs", ReadOnly: true, Directory: located("inputs", domain.BackendJuiceFS, domain.LifetimePermanent, "subject", "case:1")},
		{Path: "/workspace/knowledge", ReadOnly: true, Directory: located("knowledge", domain.BackendJuiceFS, domain.LifetimePermanent, "project", "p1")},
	}, work.LocatedLayout.Mounts)
	inputs := located("inputs", domain.BackendJuiceFS, domain.LifetimePermanent, "subject", "case:1")
	require.Equal(t, &inputs, work.LocatedLayout.Inputs)
	require.Equal(t, located("artifacts", domain.BackendJuiceFS, domain.LifetimePermanent, "subject", "case:1"), work.LocatedLayout.PublicationTarget)
}

// A Work without a Subject or a Project reads its own inputs, mounts no
// knowledge, and publishes into its own Artifacts, which outlive it.
func TestAWorkWithoutSubjectOrProjectLocatesItsOwnFiles(t *testing.T) {
	definition := employeeDefinition(t, "jack")
	request := layoutCreation(definition, "jack", false)
	request.StartingUser = "jack"
	work, _, err := definition.CreateWork(request, domain.WorkCreationFacts{})
	require.NoError(t, err)

	inputs, ok := mountAt(work.LocatedLayout, "/workspace/inputs")
	require.True(t, ok)
	require.Equal(t, located("inputs", domain.BackendJuiceFS, domain.LifetimePermanent, "work", work.ID), inputs.Directory)
	_, ok = mountAt(work.LocatedLayout, "/workspace/knowledge")
	require.False(t, ok, "a mount following a fact the Work lacks is left out")
	require.Len(t, work.LocatedLayout.Mounts, 4)
	require.Equal(t, located("artifacts", domain.BackendJuiceFS, domain.LifetimePermanent, "work", work.ID), work.LocatedLayout.PublicationTarget)
	spaces, err := work.PersonalFileSpaces()
	require.NoError(t, err)
	require.Equal(t, inputs.Directory, spaces.Inputs)
	require.Equal(t, work.LocatedLayout.PublicationTarget, spaces.Artifacts)
}

// A Locator follows the represented person and the starter as distinct
// facts: a Work another person starts locates the represented person's
// Directory and the starter's own, an automated start has no starter, and a
// definition representing no person gives its Works no person.
func TestTheRepresentedPersonAndTheStarterAreDistinctFacts(t *testing.T) {
	follow := func(path, name string, conditions ...domain.LocatorCondition) domain.LayoutMount {
		return domain.LayoutMount{Path: path, Locators: []domain.DirectoryLocator{{Conditions: conditions, Name: name, Backend: domain.BackendNAS, Lifetime: domain.LifetimePermanent}}}
	}
	layout := domain.DefaultWorkLayout()
	layout.Mounts = append(layout.Mounts,
		follow("$HOME/.config/person", "person-config", domain.ConditionPerson),
		follow("$HOME/.config/starter", "starter-config", domain.ConditionStarter),
		follow("$HOME/.config/per-project", "project-config", domain.ConditionPerson, domain.ConditionProject))

	definition := employeeDefinition(t, "jack")
	definition.WorkLayout = layout
	request := layoutCreation(definition, "jack", false)
	request.StartingUser, request.Subject, request.ProjectID = "jill", domain.WorkSubject{Kind: "case", ID: "1"}, "p1"
	work, _, err := definition.CreateWork(request, domain.WorkCreationFacts{})
	require.NoError(t, err)
	person, _ := mountAt(work.LocatedLayout, "$HOME/.config/person")
	require.Equal(t, owner("person", "jack"), person.Directory.Directory.Owner)
	starter, _ := mountAt(work.LocatedLayout, "$HOME/.config/starter")
	require.Equal(t, owner("starter", "jill"), starter.Directory.Directory.Owner)
	perProject, _ := mountAt(work.LocatedLayout, "$HOME/.config/per-project")
	require.Equal(t, owner("person", "jack", "project", "p1"), perProject.Directory.Directory.Owner, "facts are ordered by condition name")
	require.Equal(t, []domain.DirectoryMount{
		{Directory: located("home", domain.BackendNAS, domain.LifetimeWork, "work", work.ID), MountPoints: []string{".claude", ".config/person", ".config/starter", ".config/per-project"}},
		{Directory: located("workspace", domain.BackendNAS, domain.LifetimeWork, "work", work.ID), MountPoints: []string{"inputs", "knowledge"}},
		{Directory: located("claude-code", domain.BackendNAS, domain.LifetimePermanent, "agent_definition", definition.ID)},
		{Directory: located("inputs", domain.BackendJuiceFS, domain.LifetimePermanent, "subject", "case:1"), ReadOnly: true},
		{Directory: located("knowledge", domain.BackendJuiceFS, domain.LifetimePermanent, "project", "p1"), ReadOnly: true},
		{Directory: person.Directory}, {Directory: starter.Directory}, {Directory: perProject.Directory},
	}, work.LocatedLayout.MountRequests(), "a start requests exactly the located Directories, each with the mount points nested in it")

	request.StartingUser = ""
	automated, _, err := definition.CreateWork(request, domain.WorkCreationFacts{})
	require.NoError(t, err)
	_, ok := mountAt(automated.LocatedLayout, "$HOME/.config/starter")
	require.False(t, ok, "an automated start has no starter")

	project := projectDefinition(t, "p1")
	project.WorkLayout = layout
	request = layoutCreation(project, "pm", false)
	request.StartingUser, request.Subject, request.ProjectID = "pm", domain.WorkSubject{Kind: "project", ID: "p1"}, "p1"
	coordination, _, err := project.CreateWork(request, domain.WorkCreationFacts{})
	require.NoError(t, err)
	_, ok = mountAt(coordination.LocatedLayout, "$HOME/.config/person")
	require.False(t, ok, "a definition representing no person gives its Works no person")
	starter, ok = mountAt(coordination.LocatedLayout, "$HOME/.config/starter")
	require.True(t, ok)
	require.Equal(t, owner("starter", "pm"), starter.Directory.Directory.Owner)
}

// The Agent Harness places its state mount; a layout holding another mount at
// that path refuses the creation, and an unknown Harness is refused.
func TestAHarnessStatePathTakenByAnotherMountRefusesCreation(t *testing.T) {
	definition := employeeDefinition(t, "jack")
	layout := domain.DefaultWorkLayout()
	layout.Mounts = append(layout.Mounts, domain.LayoutMount{Path: "$HOME/.claude", Locators: []domain.DirectoryLocator{{Conditions: []domain.LocatorCondition{domain.ConditionPerson}, Name: "claude", Backend: domain.BackendNAS, Lifetime: domain.LifetimePermanent}}})
	definition.WorkLayout = layout
	require.NoError(t, definition.Validate(), "the layout itself has one mount per path")
	_, _, err := definition.CreateWork(layoutCreation(definition, "jack", false), domain.WorkCreationFacts{})
	require.ErrorIs(t, err, domain.ErrInvalidWork)

	definition.WorkLayout = domain.DefaultWorkLayout()
	request := layoutCreation(definition, "jack", false)
	request.AgentHarness = "codex"
	_, _, err = definition.CreateWork(request, domain.WorkCreationFacts{})
	require.ErrorIs(t, err, domain.ErrUnknownAgentHarness)
}

// A Work fixes its located layout and Agent Harness at creation: a later
// change of its definition's Work Layout never moves a Work's Directories,
// and every start launches with what the Work fixed.
func TestAWorkKeepsItsLocatedLayoutWhenItsDefinitionChanges(t *testing.T) {
	definition := employeeDefinition(t, "jack")
	request := layoutCreation(definition, "jack", false)
	request.StartingUser, request.Subject, request.ProjectID = "jack", domain.WorkSubject{Kind: "case", ID: "1"}, "p1"
	work, _, err := definition.CreateWork(request, domain.WorkCreationFacts{})
	require.NoError(t, err)
	fixed := work.LocatedLayout.Clone()

	changed := domain.DefaultWorkLayout()
	changed.Mounts[2].Locators[0].Conditions = []domain.LocatorCondition{domain.ConditionPerson}
	changed.Mounts = changed.Mounts[:4]
	definition.WorkLayout = changed
	require.NoError(t, definition.Validate())

	reused, created, err := definition.CreateWork(request, domain.WorkCreationFacts{SubjectWork: work})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, fixed, reused.LocatedLayout)
	require.Equal(t, domain.ClaudeCode.ID, reused.AgentHarnessReference)
	launch := work.LaunchSpecification()
	require.Equal(t, fixed, launch.LocatedLayout)
	require.Equal(t, domain.ClaudeCode.ID, launch.AgentHarness)
}

// A Work on a Managed Computer mounts no Directory: its files stay on the
// machine, which copies its located inputs, and it still publishes into its
// publication target.
func TestAManagedWorkMountsNoDirectory(t *testing.T) {
	definition := employeeDefinition(t, "jack")
	request := layoutCreation(definition, "jack", true)
	request.StartingUser = "jack"
	work, _, err := definition.CreateWork(request, domain.WorkCreationFacts{})
	require.NoError(t, err)
	require.Empty(t, work.LocatedLayout.Mounts)
	require.Empty(t, work.LocatedLayout.MountRequests())
	inputs := located("inputs", domain.BackendJuiceFS, domain.LifetimePermanent, "work", work.ID)
	require.Equal(t, &inputs, work.LocatedLayout.Inputs)
	require.Equal(t, located("artifacts", domain.BackendJuiceFS, domain.LifetimePermanent, "work", work.ID), work.LocatedLayout.PublicationTarget)
	_, ok := work.LocatedLayout.Workspace()
	require.False(t, ok)
}
