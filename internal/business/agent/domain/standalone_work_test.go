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

// standaloneWork is a Work whose Work Caller is its owner, with its own File
// Space, under an employee definition owned by "owner".
func standaloneWork(t *testing.T) visibleWork {
	t.Helper()
	definition := newDefinition(t, "employee", domain.VisibilityRule{})
	work, err := domain.NewAgentWork(domain.WorkInput{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(), AgentID: definition.ID,
		WorkCaller:   domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "owner"},
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, DisplayTitle: "Draft the plan",
		RepresentedPerson: "", WorkLayout: domain.DefaultWorkLayout(), Visibility: definition.Visibility,
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	require.True(t, work.Standalone())
	return visibleWork{AgentWork: work, definition: definition}
}

func TestDisplayTitleStartsFromTheFirstLineOfTheFirstInput(t *testing.T) {
	require.Equal(t, "Draft the plan", domain.DisplayTitleFrom("  Draft the plan  \nwith details"))
	require.Equal(t, strings.Repeat("长", 79)+"…", domain.DisplayTitleFrom(strings.Repeat("长", 250)), "a long first line is cut to 80 characters with an ellipsis")
	require.Equal(t, strings.Repeat("长", 80), domain.DisplayTitleFrom(strings.Repeat("长", 80)), "a first line of 80 characters is kept whole")
	require.Equal(t, strings.Repeat("a", 78)+"…", domain.DisplayTitleFrom(strings.Repeat("a", 78)+" tail of the instruction"), "no space is left before the ellipsis")
}

func TestOnlyAStandaloneWorkHasADisplayTitle(t *testing.T) {
	work := humanWork(t)
	_, err := work.ChangeDisplayTitle(work.definition, "owner", "Renamed")
	require.ErrorIs(t, err, domain.ErrNotStandaloneWork)
}

func TestOwnerRenamesTheirStandaloneWork(t *testing.T) {
	work := standaloneWork(t)
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

func TestOnlyTheOwnerChangesAStandaloneWorksFilesUntilItCloses(t *testing.T) {
	work := standaloneWork(t)
	require.NoError(t, work.AdmitFileChange(work.definition, "owner"))
	require.ErrorIs(t, work.AdmitFileChange(work.definition, "reader"), domain.ErrWorkFileNotAdmitted)

	work.State = domain.WorkClosed
	require.ErrorIs(t, work.AdmitFileChange(work.definition, "owner"), domain.ErrWorkFilesClosed)
	require.NoError(t, work.AdmitFileSharing(work.definition, "owner"), "Share Links stay the owner's after closing")
	require.ErrorIs(t, work.AdmitFileSharing(work.definition, "reader"), domain.ErrWorkFileNotAdmitted)

	other := humanWork(t)
	require.ErrorIs(t, other.AdmitFileChange(other.definition, "owner"), domain.ErrWorkFilesUnavailable, "an Extension Work's files are its extension's")
}

func TestOwnerAndWorkShareHoldersReadAWorksFiles(t *testing.T) {
	work := standaloneWork(t)
	_, err := work.GrantWorkShare(work.definition, "owner", "reader", member)
	require.ErrorIs(t, err, domain.ErrWorkShareOutOfScope, "a Standalone Work is shared within the User Directory only")
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
	require.ErrorIs(t, err, domain.ErrWorkShareOutOfScope, "an Extension Work is shared among its Work Caller's members only")
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
	require.Equal(t, "talgent:work-executor", definition.WorkDefaults.StartupAgent)
	require.Empty(t, definition.ProvideCapabilities().RequiredPlugins, "the deployment's Built-in Plugins supply the core plugin")
}

// A Work starts only from another Work of its definition and Work Caller: a
// Standalone Work never from one of its definition's Extension Works.
func TestAStandaloneWorkStartsOnlyFromAnotherStandaloneWork(t *testing.T) {
	definition := newDefinition(t, "employee", domain.VisibilityRule{})
	owner := domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "owner"}
	request := func(caller domain.WorkCaller, key domain.WorkKey, source *domain.StartingContextSource) domain.WorkCreationRequest {
		return domain.WorkCreationRequest{StartedBy: "owner", Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true},
			WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, WorkCaller: caller, WorkKey: key, StartingContext: source,
			Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}}
	}
	extensionWork, _, err := definition.CreateWork(request(projectA, domain.WorkKey{Kind: "case", ID: "case-1"}, nil), domain.WorkCreationFacts{})
	require.NoError(t, err)
	earlier, _, err := definition.CreateWork(request(owner, domain.WorkKey{}, nil), domain.WorkCreationFacts{})
	require.NoError(t, err)
	lookup := &definitionSessionLookup{works: []*domain.AgentWork{extensionWork, earlier}}

	_, _, err = createWork(definition, request(owner, domain.WorkKey{}, &domain.StartingContextSource{WorkID: extensionWork.ID, Position: "message"}), lookup)
	require.ErrorIs(t, err, domain.ErrStartingContextNotAdmitted)
	_, _, err = createWork(definition, request(domain.WorkCaller{Kind: "project", ID: "project-b"}, domain.WorkKey{}, &domain.StartingContextSource{WorkID: extensionWork.ID, Position: "message"}), lookup)
	require.ErrorIs(t, err, domain.ErrStartingContextNotAdmitted, "nor from a Work of another extension Work Caller")
	continued, _, err := createWork(definition, request(owner, domain.WorkKey{}, &domain.StartingContextSource{WorkID: earlier.ID, Position: "message"}), lookup)
	require.NoError(t, err)
	require.Equal(t, earlier.ID, continued.StartingContext.SourceWorkID)
	sibling, _, err := createWork(definition, request(projectA, domain.WorkKey{}, &domain.StartingContextSource{WorkID: extensionWork.ID, Position: "message"}), lookup)
	require.NoError(t, err)
	require.Equal(t, extensionWork.ID, sibling.StartingContext.SourceWorkID)
}

func TestNoExtensionActsOnAStandaloneWork(t *testing.T) {
	require.ErrorIs(t, standaloneWork(t).AdmitDelegatedControl(), domain.ErrStandaloneWorkOwnerOnly)
	require.NoError(t, humanWork(t).AdmitDelegatedControl())
}

// Readers read every file of a Work's own File Spaces, and in its File
// Home's only the Artifacts the Work published.
func TestSharedSpaceReading(t *testing.T) {
	require.True(t, domain.ReadableWorkFile("w", false, false, ""), "an input in the Work's own space")
	require.True(t, domain.ReadableWorkFile("w", true, true, "w"), "its own Artifact in its File Home")
	require.False(t, domain.ReadableWorkFile("w", true, true, "other"), "another Work's Artifact in the File Home")
	require.False(t, domain.ReadableWorkFile("w", true, false, "w"), "an input of the File Home")
}
