//go:build integration

package infrastructure_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"xorm.io/xorm"
)

func TestDefinitionPurposeLookupPreservesScopeAndConcurrentUniqueness(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	lookup := query.NewGetAgentDefinitionForPurposeHandler(infrastructure.NewAgentDefinitionQueryRepository(db))
	for _, selection := range []query.GetAgentDefinitionForPurpose{
		{ScopeKind: "employee", ScopeReference: "alice", Purpose: "work"},
		{ScopeKind: "employee", ScopeReference: "bob", Purpose: "work"},
		{ScopeKind: "project", ScopeReference: "project-a", Purpose: "project_coordination"},
		{ScopeKind: "project", ScopeReference: "project-b", Purpose: "project_coordination"},
		{ScopeKind: "project", ScopeReference: "project-a", Purpose: "caller-defined"},
	} {
		definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: selection.Purpose, UsageScope: domain.UsageScope{Kind: selection.ScopeKind, Reference: selection.ScopeReference}})
		require.NoError(t, err)
		require.NoError(t, agents.Save(ctx, definition))
		id, err := lookup.Handle(ctx, selection)
		require.NoError(t, err)
		require.Equal(t, definition.ID, id)
	}
	_, err = lookup.Handle(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: "employee", ScopeReference: "project-a", Purpose: "caller-defined"})
	require.ErrorIs(t, err, domain.ErrAgentDefinitionNotFound, "same reference and purpose cannot cross scope kinds")
	_, err = lookup.Handle(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: "project", ScopeReference: "project-b", Purpose: "caller-defined"})
	require.ErrorIs(t, err, domain.ErrAgentDefinitionNotFound, "a missing project definition must not fall back to another project")

	const contenders = 4
	start := make(chan struct{})
	results := make(chan error, contenders)
	var ready sync.WaitGroup
	ready.Add(contenders)
	for range contenders {
		definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "concurrent"}})
		require.NoError(t, err)
		go func() { ready.Done(); <-start; results <- agents.Save(ctx, definition) }()
	}
	ready.Wait()
	close(start)
	succeeded := 0
	for range contenders {
		if err := <-results; err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, domain.ErrConcurrentModification)
		}
	}
	require.Equal(t, 1, succeeded, "one definition owns the scope-purpose identity")
	selection := query.GetAgentDefinitionForPurpose{ScopeKind: "employee", ScopeReference: "concurrent", Purpose: "work"}
	winner, err := lookup.Handle(ctx, selection)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	retained, err := query.NewGetAgentDefinitionForPurposeHandler(infrastructure.NewAgentDefinitionQueryRepository(db)).Handle(ctx, selection)
	require.NoError(t, err)
	require.Equal(t, winner, retained)
}

// Intent (real): a caller's lists and comments show many Agents; one read by
// employee returns only employee-scoped work definitions, and one read by
// Work returns each Work's definition.
func TestAgentAppearancesReadEmployeeWorkAgentsAndWorkAgents(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	definitions := map[string]*domain.AgentDefinition{}
	for _, fixture := range []struct{ name, kind, reference, purpose string }{
		{"Alice's Agent", "employee", "alice", "work"},
		{"Bob's Agent", "employee", "bob", "work"},
		{"Carol Other", "employee", "carol", "other"},
		{"Project Dave", "project", "dave", "work"},
	} {
		definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{fixture.reference}, AgentName: fixture.name, Purpose: fixture.purpose, UsageScope: domain.UsageScope{Kind: fixture.kind, Reference: fixture.reference}})
		require.NoError(t, err)
		require.NoError(t, agents.Save(ctx, definition))
		definitions[fixture.reference] = definition
	}
	rocket, err := domain.NewAgentIcon("rocket", "violet")
	require.NoError(t, err)
	bob, err := agents.Get(ctx, definitions["bob"].ID)
	require.NoError(t, err)
	_, err = bob.ChangeAppearance(domain.DefinitionRequester{ID: "bob"}, "Scout", rocket)
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, bob))

	lookup := infrastructure.NewAgentDefinitionQueryRepository(db)
	views, err := lookup.ListEmployeeAgents(ctx, "work", []string{"alice", "bob", "carol", "dave", "nobody"})
	require.NoError(t, err)
	require.Equal(t, map[string]query.AgentAppearanceView{
		"alice": {AgentID: definitions["alice"].ID, Appearance: domain.AgentAppearance{Name: "Alice's Agent", Icon: domain.DefaultAgentIcon}},
		"bob":   {AgentID: definitions["bob"].ID, Appearance: domain.AgentAppearance{Name: "Scout", Icon: rocket}},
	}, views)
	views, err = lookup.ListEmployeeAgents(ctx, "other", []string{"alice", "carol"})
	require.NoError(t, err)
	require.Len(t, views, 1, "the caller's purpose selects the definition")
	require.Equal(t, "Carol Other", views["carol"].Appearance.Name)
	views, err = lookup.ListEmployeeAgents(ctx, "work", nil)
	require.NoError(t, err)
	require.Empty(t, views)

	_, err = db.Exec("INSERT INTO agent_session (id, agent_id, started_by, work_key_kind, work_key_id, work_caller_kind, work_caller_id, state, capabilities, context_ref, agent_harness, located_layout, trace_attributes, harness_process, failure_reason, represented_person, version, created_at, updated_at, deleted_at) VALUES (?, ?, 'bob', 'case', 'bob-work', 'project', 'project', 1, '{}', '', 'claude-code', '{\"mounts\":[]}', '{}', '{}', '', 'bob', 1, 0, 0, 0)", "work-bob", definitions["bob"].ID)
	require.NoError(t, err)
	views, err = lookup.ListWorkAgents(ctx, []string{"work-bob", "work-missing"})
	require.NoError(t, err)
	require.Equal(t, map[string]query.AgentAppearanceView{"work-bob": {AgentID: definitions["bob"].ID, Appearance: domain.AgentAppearance{Name: "Scout", Icon: rocket}}}, views)
}
