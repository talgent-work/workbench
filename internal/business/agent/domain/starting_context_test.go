package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func pmDefinition(t *testing.T) *domain.AgentDefinition {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "project_coordination",
		UsageScope:   domain.UsageScope{Kind: "project", Reference: "project-a"},
		Capabilities: domain.CapabilityConfiguration{Prompt: "Base", StartupAgent: "pm:coordinator"},
	})
	require.NoError(t, err)
	return agent
}

func startingRequest(agent *domain.AgentDefinition, subjectID string, source *domain.StartingContextSource) domain.WorkCreationRequest {
	return domain.WorkCreationRequest{StartedBy: "owner", Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true},
		WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, Subject: domain.WorkSubject{Kind: "review", ID: subjectID}, ProjectID: "project-a",
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, StartingContext: source}
}

// A Work starts from another Work's context only when that Work belongs to
// the same definition and Project; it keeps the source's Harness Session and
// the named position, and its start request carries them.
func TestWorkStartsFromAnotherWorkOfItsDefinitionOnly(t *testing.T) {
	agent := pmDefinition(t)
	source, _, err := agent.CreateWork(startingRequest(agent, "project-work", nil), domain.WorkCreationFacts{})
	require.NoError(t, err)
	require.Nil(t, source.StartingContext)

	other := pmDefinition(t)
	foreign, _, err := other.CreateWork(startingRequest(other, "project-work", nil), domain.WorkCreationFacts{})
	require.NoError(t, err)
	lookup := &definitionSessionLookup{works: []*domain.AgentWork{source, foreign}}
	derived, created, err := createWork(agent, startingRequest(agent, "review-1", &domain.StartingContextSource{WorkID: source.ID, Position: " message-uuid "}), lookup)
	port := follow(derived)
	require.NoError(t, err)
	require.True(t, created)
	want := &domain.StartingContext{SourceWorkID: source.ID, SourceSessionReference: source.HarnessSessionReference, Position: "message-uuid"}
	require.Equal(t, want, derived.StartingContext)
	require.Equal(t, 1, port.starts())
	require.Equal(t, want, port.launch().StartingContext, "its first start takes up the starting context")

	for name, source := range map[string]*domain.StartingContextSource{
		"another definition": {WorkID: foreign.ID, Position: "message-uuid"},
		"no position":        {WorkID: source.ID, Position: " "},
		"no Work":            {WorkID: "missing", Position: "message-uuid"},
	} {
		work, _, err := createWork(agent, startingRequest(agent, "review-2", source), lookup)
		require.ErrorIs(t, err, domain.ErrStartingContextNotAdmitted, name)
		require.Nil(t, work, "a refused starting context creates no Work")
	}
}
