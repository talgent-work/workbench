package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// visibleWork is a Work with the definition whose Owners govern it.
type visibleWork struct {
	*domain.AgentWork
	definition *domain.AgentDefinition
}

func newDefinition(t *testing.T, scope string, rule domain.VisibilityRule) *domain.AgentDefinition {
	t.Helper()
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: scope, Reference: "owner"}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, Visibility: rule})
	require.NoError(t, err)
	return definition
}

func newVisibleWork(t *testing.T, definition *domain.AgentDefinition) visibleWork {
	t.Helper()
	work, err := domain.NewAgentWork(domain.WorkInput{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(), AgentID: definition.ID, Subject: domain.WorkSubject{Kind: "case", ID: "case-a"}, ProjectID: "project-a", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}},
		WorkspaceReference: "workspace-a", HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, Visibility: definition.Visibility,
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	work.Events.Drain() // Only the events of sharing and visibility are read here.
	return visibleWork{AgentWork: work, definition: definition}
}

func humanWork(t *testing.T) visibleWork {
	return newVisibleWork(t, newDefinition(t, "employee", domain.VisibilityRule{}))
}

var member = domain.ReaderAdmission{ProjectID: "project-a"}

func drained(work visibleWork) []domain.Event { return work.Events.Drain() }

func TestDefinitionDeclaresTheVisibilityItsWorksCapture(t *testing.T) {
	input := domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "project_coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"}, HomeBinding: domain.HomeBinding{OwnerID: "pm", Namespace: "pm"}, Visibility: domain.VisibilityRule{Public: true}}
	definition, err := domain.NewAgentDefinition(input)
	require.NoError(t, err)
	require.False(t, definition.RepresentsHuman())
	require.True(t, newVisibleWork(t, definition).Visibility.Public, "a Work captures its definition's visibility within its Project")
}

func TestOwnerSwitchesAHumansWorkBetweenPrivateAndPublic(t *testing.T) {
	work := humanWork(t)
	_, err := work.ChangeVisibility(work.definition, "someone", true, time.Now())
	require.ErrorIs(t, err, domain.ErrControlRequesterNotOwner)
	_, err = work.ChangeVisibility(newDefinition(t, "employee", domain.VisibilityRule{}), "owner", true, time.Now())
	require.ErrorIs(t, err, domain.ErrControlRequesterNotOwner, "another definition's Owners do not govern this Work")

	changed, err := work.ChangeVisibility(work.definition, "owner", true, time.Now())
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, work.Visibility.Public)
	require.Zero(t, work.Events.Len(), "becoming public withdraws no reader")

	_, err = work.GrantWorkShare(work.definition, "owner", "reader", member)
	require.NoError(t, err)
	changed, err = work.ChangeVisibility(work.definition, "owner", false, time.Now())
	require.NoError(t, err)
	require.True(t, changed)
	events := drained(work)
	require.Len(t, events, 1)
	private := events[0].(domain.WorkMadePrivate)
	require.Equal(t, work.ID, private.WorkID)
	require.Equal(t, []string{"owner"}, private.Owners)
	require.Equal(t, []string{"reader"}, private.ShareReaderIDs)
}

// Transferring a definition's Owners changes who governs every one of its Works.
func TestOwnerTransferChangesWhoGovernsEveryWork(t *testing.T) {
	definition := newDefinition(t, "employee", domain.VisibilityRule{})
	works := []visibleWork{newVisibleWork(t, definition), newVisibleWork(t, definition)}
	changed, err := definition.TransferOwners([]string{"successor"})
	require.NoError(t, err)
	require.True(t, changed)
	for _, work := range works {
		require.ErrorIs(t, work.AdmitControlRequester(definition, "owner"), domain.ErrControlRequesterNotOwner)
		require.NoError(t, work.AdmitControlRequester(definition, "successor"))
		basis, err := work.AdmitStreamReader(definition, "successor", domain.ReaderAdmission{})
		require.NoError(t, err)
		require.Equal(t, domain.ReadAsOwner, basis)
		_, err = work.AdmitStreamReader(definition, "owner", domain.ReaderAdmission{})
		require.ErrorIs(t, err, domain.ErrWorkReaderNotAdmitted)
	}
}

func TestPMCoordinatorsWorkKeepsItsDeclaredVisibility(t *testing.T) {
	work := newVisibleWork(t, newDefinition(t, "project", domain.VisibilityRule{Public: true}))
	_, err := work.ChangeVisibility(work.definition, "owner", false, time.Now())
	require.ErrorIs(t, err, domain.ErrWorkVisibilityFixed)
	_, err = work.GrantWorkShare(work.definition, "owner", "reader", member)
	require.ErrorIs(t, err, domain.ErrWorkVisibilityFixed)
}

func TestWorkShareGrantIsIdempotentAndRevocationIsRecorded(t *testing.T) {
	work := humanWork(t)
	_, err := work.GrantWorkShare(work.definition, "owner", "outsider", domain.ReaderAdmission{ProjectID: "project-b"})
	require.ErrorIs(t, err, domain.ErrWorkShareOutOfScope)
	_, err = work.GrantWorkShare(work.definition, "reader", "other", member)
	require.ErrorIs(t, err, domain.ErrControlRequesterNotOwner)
	_, err = work.GrantWorkShare(work.definition, "owner", "owner", member)
	require.ErrorIs(t, err, domain.ErrInvalidVisibility)

	changed, err := work.GrantWorkShare(work.definition, "owner", "reader", member)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = work.GrantWorkShare(work.definition, "owner", "reader", member)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, []string{"reader"}, work.WorkShares)

	_, err = work.RevokeWorkShare(work.definition, "reader", "reader", time.Now())
	require.ErrorIs(t, err, domain.ErrControlRequesterNotOwner)
	changed, err = work.RevokeWorkShare(work.definition, "owner", "reader", time.Now())
	require.NoError(t, err)
	require.True(t, changed)
	require.Empty(t, work.WorkShares)
	events := drained(work)
	require.Equal(t, []domain.Event{domain.WorkShareRevoked{WorkID: work.ID, ReaderID: "reader", OccurredAt: events[0].(domain.WorkShareRevoked).OccurredAt}}, events)
}

func TestRevokingAnAbsentShareChangesNothing(t *testing.T) {
	work := humanWork(t)
	changed, err := work.RevokeWorkShare(work.definition, "owner", "reader", time.Now())
	require.NoError(t, err)
	require.False(t, changed)
	require.Zero(t, work.Events.Len())
}

func TestDepartedReaderLosesItsWorkShare(t *testing.T) {
	work := humanWork(t)
	_, err := work.GrantWorkShare(work.definition, "owner", "reader", member)
	require.NoError(t, err)
	require.False(t, work.RevokeDepartedReader("reader", domain.ReaderAdmission{ProjectID: "other-project"}, time.Now()), "leaving another Project keeps the share")
	departed := domain.ReaderAdmission{ProjectID: work.ProjectID}
	require.True(t, work.RevokeDepartedReader("reader", departed, time.Now()))
	require.False(t, work.RevokeDepartedReader("reader", departed, time.Now()))
	require.Len(t, drained(work), 1)
}

func TestStreamReaderAdmission(t *testing.T) {
	private := humanWork(t)
	_, err := private.GrantWorkShare(private.definition, "owner", "reader", member)
	require.NoError(t, err)
	public := func() visibleWork {
		return newVisibleWork(t, newDefinition(t, "employee", domain.VisibilityRule{Public: true}))
	}

	for _, tc := range []struct {
		name      string
		work      visibleWork
		reader    string
		admission domain.ReaderAdmission
		basis     domain.ReadBasis
	}{
		{"owner", private, "owner", domain.ReaderAdmission{}, domain.ReadAsOwner},
		{"share holder of a private Work", private, "reader", domain.ReaderAdmission{}, domain.ReadAsShare},
		{"member of a private Work", private, "member", member, ""},
		{"public Work member", public(), "member", member, domain.ReadAsPublic},
		{"public Work without admission", public(), "member", domain.ReaderAdmission{}, ""},
		{"public Work admitted for another Project", public(), "member", domain.ReaderAdmission{ProjectID: "project-b"}, ""},
		{"anonymous", private, "", domain.ReaderAdmission{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			basis, err := tc.work.AdmitStreamReader(tc.work.definition, tc.reader, tc.admission)
			if tc.basis == "" {
				require.ErrorIs(t, err, domain.ErrWorkReaderNotAdmitted)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.basis, basis)
		})
	}
}
