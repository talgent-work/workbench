package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// personalWork is a Work without a Subject or a Project, with its own File
// Space, under an employee definition owned by "owner".
func personalWork(t *testing.T) visibleWork {
	t.Helper()
	definition := newDefinition(t, "employee", domain.VisibilityRule{})
	work, err := domain.NewAgentWork(domain.WorkInput{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(), AgentID: definition.ID,
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, DisplayTitle: "Draft the plan",
		RepresentedPerson: "", WorkLayout: domain.DefaultWorkLayout(), Visibility: definition.Visibility,
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	require.True(t, work.Personal())
	return visibleWork{AgentWork: work, definition: definition}
}

func TestDisplayTitleStartsFromTheFirstLineOfTheFirstInput(t *testing.T) {
	require.Equal(t, "Draft the plan", domain.DisplayTitleFrom("  Draft the plan  \nwith details"))
	require.Equal(t, strings.Repeat("长", 200), domain.DisplayTitleFrom(strings.Repeat("长", 250)), "a long first line is cut to 200 characters")
}

func TestOnlyAPersonalWorkHasADisplayTitle(t *testing.T) {
	work := humanWork(t)
	_, err := work.ChangeDisplayTitle(work.definition, "owner", "Renamed")
	require.ErrorIs(t, err, domain.ErrNotPersonalWork)
}

func TestOwnerRenamesTheirPersonalWork(t *testing.T) {
	work := personalWork(t)
	_, err := work.ChangeDisplayTitle(work.definition, "reader", "Renamed")
	require.ErrorIs(t, err, domain.ErrControlRequesterNotOwner)
	for _, invalid := range []string{"  ", "two\nlines", strings.Repeat("x", 201)} {
		_, err = work.ChangeDisplayTitle(work.definition, "owner", invalid)
		require.ErrorIs(t, err, domain.ErrInvalidDisplayTitle, invalid)
	}
	changed, err := work.ChangeDisplayTitle(work.definition, "owner", " Renamed ")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "Renamed", work.DisplayTitle)
	changed, err = work.ChangeDisplayTitle(work.definition, "owner", "Renamed")
	require.NoError(t, err)
	require.False(t, changed)
}

func TestOnlyTheOwnerChangesAPersonalWorksFilesUntilItCloses(t *testing.T) {
	work := personalWork(t)
	require.NoError(t, work.AdmitFileChange(work.definition, "owner"))
	require.ErrorIs(t, work.AdmitFileChange(work.definition, "reader"), domain.ErrWorkFileNotAdmitted)

	work.State = domain.WorkClosed
	require.ErrorIs(t, work.AdmitFileChange(work.definition, "owner"), domain.ErrWorkFilesClosed)
	require.NoError(t, work.AdmitFileSharing(work.definition, "owner"), "Share Links stay the owner's after closing")
	require.ErrorIs(t, work.AdmitFileSharing(work.definition, "reader"), domain.ErrWorkFileNotAdmitted)

	other := humanWork(t)
	require.ErrorIs(t, other.AdmitFileChange(other.definition, "owner"), domain.ErrWorkFilesUnavailable, "a Work with a Subject or a Project has its caller's files")
}

func TestOwnerAndWorkShareHoldersReadAWorksFiles(t *testing.T) {
	work := personalWork(t)
	_, err := work.GrantWorkShare(work.definition, "owner", "reader", member)
	require.ErrorIs(t, err, domain.ErrWorkShareOutOfScope, "a Work without a Project is shared within the User Directory only")
	_, err = work.GrantWorkShare(work.definition, "owner", "reader", domain.ReaderAdmission{Directory: true})
	require.NoError(t, err)

	basis, err := work.AdmitFileReader(work.definition, "owner")
	require.NoError(t, err)
	require.Equal(t, domain.ReadAsOwner, basis)
	basis, err = work.AdmitFileReader(work.definition, "reader")
	require.NoError(t, err)
	require.Equal(t, domain.ReadAsShare, basis)
	_, err = work.AdmitFileReader(work.definition, "stranger")
	require.ErrorIs(t, err, domain.ErrWorkReaderNotAdmitted)

	withProject := humanWork(t)
	_, err = withProject.GrantWorkShare(withProject.definition, "owner", "reader", domain.ReaderAdmission{Directory: true})
	require.ErrorIs(t, err, domain.ErrWorkShareOutOfScope, "a Work with a Project is shared within its Project only")
}

func TestAnEmployeeDefinitionRepresentsItsPersonPrivately(t *testing.T) {
	definition, err := domain.NewAgentDefinition(domain.EmployeeDefinitionInput("jack"))
	require.NoError(t, err)
	require.Equal(t, []string{"jack"}, definition.Owners)
	require.Equal(t, domain.UsageScope{Kind: "employee", Reference: "jack"}, definition.UsageScope)
	require.Equal(t, "work", definition.Purpose)
	require.Equal(t, "jack", definition.RepresentedPerson())
	require.True(t, definition.RepresentsHuman())
	require.False(t, definition.Visibility.Public, "its Works start private")
	require.Equal(t, "talgent:personal-work-executor", definition.ProvideCapabilities().StartupAgent)
	require.Empty(t, definition.ProvideCapabilities().RequiredPlugins, "the deployment's Built-in Plugins supply the core plugin")
}

// A Personal Work starts from another Personal Work of its definition, never
// from one of its definition's Works with a Project.
func TestAPersonalWorkStartsOnlyFromAnotherPersonalWork(t *testing.T) {
	definition := newDefinition(t, "employee", domain.VisibilityRule{})
	request := func(subject domain.WorkSubject, project string, source *domain.StartingContextSource) domain.WorkCreationRequest {
		return domain.WorkCreationRequest{StartedBy: "owner", Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true},
			WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, Subject: subject, ProjectID: project, StartingContext: source,
			Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}}
	}
	intentWork, _, err := definition.CreateWork(request(domain.WorkSubject{Kind: "case", ID: "case-1"}, "project-a", nil), domain.WorkCreationFacts{})
	require.NoError(t, err)
	earlier, _, err := definition.CreateWork(request(domain.WorkSubject{}, "", nil), domain.WorkCreationFacts{})
	require.NoError(t, err)
	lookup := &definitionSessionLookup{works: []*domain.AgentWork{intentWork, earlier}}

	_, _, err = createWork(definition, request(domain.WorkSubject{}, "", &domain.StartingContextSource{WorkID: intentWork.ID, Position: "message"}), lookup)
	require.ErrorIs(t, err, domain.ErrStartingContextNotAdmitted)
	continued, _, err := createWork(definition, request(domain.WorkSubject{}, "", &domain.StartingContextSource{WorkID: earlier.ID, Position: "message"}), lookup)
	require.NoError(t, err)
	require.Equal(t, earlier.ID, continued.StartingContext.SourceWorkID)
}

func TestNoCallerCreatesOrActsOnAPersonalWork(t *testing.T) {
	require.ErrorIs(t, domain.AdmitCallerCreation(domain.WorkSubject{}, ""), domain.ErrPersonalWorkOwnerOnly)
	require.NoError(t, domain.AdmitCallerCreation(domain.WorkSubject{}, "project-a"))
	require.NoError(t, domain.AdmitCallerCreation(domain.WorkSubject{Kind: "case", ID: "case-1"}, ""))
	require.ErrorIs(t, personalWork(t).AdmitDelegatedControl(), domain.ErrPersonalWorkOwnerOnly)
	require.NoError(t, humanWork(t).AdmitDelegatedControl())
}
