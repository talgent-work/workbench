//go:build integration

package infrastructure_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// A Work created from another Work's context keeps that source and position
// across reloads; a source Work of another definition creates nothing.
func TestStartingContextSurvivesReload(t *testing.T) {
	f := newWorkDefinitionFixture(t, "owner")
	handler := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)),
		creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), executionPort{}, creationPreparation(f.db, executionPort{}, creationModels()))
	base := f.request
	base.InitialExecution = nil
	create := func(subject string, starting *domain.StartingContextSource) (string, error) {
		creation := base
		creation.RequestID, creation.Subject, creation.StartingContext = uuid.NewString(), domain.WorkSubject{Kind: "case", ID: subject}, starting
		return handler.Handle(t.Context(), creation)
	}
	sourceID, err := create(uuid.NewString(), nil)
	require.NoError(t, err)
	source, err := f.sessions.Get(t.Context(), sourceID)
	require.NoError(t, err)
	require.Nil(t, source.StartingContext)

	derivedID, err := create(uuid.NewString(), &domain.StartingContextSource{WorkID: sourceID, Position: "native-uuid-1"})
	require.NoError(t, err)
	derived, err := f.sessions.Get(t.Context(), derivedID)
	require.NoError(t, err)
	require.Equal(t, &domain.StartingContext{SourceWorkID: sourceID, SourceSessionReference: source.HarnessSessionReference, Position: "native-uuid-1"}, derived.StartingContext)

	_, err = create(uuid.NewString(), &domain.StartingContextSource{WorkID: uuid.NewString(), Position: "native-uuid-1"})
	require.ErrorIs(t, err, domain.ErrStartingContextNotAdmitted)
}
