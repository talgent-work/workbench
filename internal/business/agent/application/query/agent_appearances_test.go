package query

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type appearanceStub struct {
	purpose string
	views   map[string]AgentAppearanceView
	err     error
	ids     []string
	reads   int
}

func (s *appearanceStub) read(_ context.Context, ids []string) (map[string]AgentAppearanceView, error) {
	s.reads++
	s.ids = ids
	return s.views, s.err
}
func (s *appearanceStub) ListEmployeeAgents(ctx context.Context, purpose string, ids []string) (map[string]AgentAppearanceView, error) {
	s.purpose = purpose
	return s.read(ctx, ids)
}
func (s *appearanceStub) ListWorkAgents(ctx context.Context, ids []string) (map[string]AgentAppearanceView, error) {
	return s.read(ctx, ids)
}

// Intent (real): callers get one result per requested employee or Work in
// order and in one read; one without an Agent is unknown, not a failure.
func TestResolveAgentsAnswersEveryReferenceInOneRead(t *testing.T) {
	alice := AgentAppearanceView{AgentID: "agent-a", Appearance: domain.AgentAppearance{Name: "Alice's Agent", Icon: domain.DefaultAgentIcon}}
	reader := &appearanceStub{views: map[string]AgentAppearanceView{"alice": alice}}
	handler := NewResolveAgentsHandler(reader)
	byEmployee := func(ctx context.Context, ids []string) ([]ResolvedAgent, error) {
		return handler.ByEmployee(ctx, " work ", ids)
	}
	for _, resolve := range []func(context.Context, []string) ([]ResolvedAgent, error){byEmployee, handler.ByWork} {
		reader.reads = 0
		results, err := resolve(t.Context(), []string{"alice", " nobody ", "", "alice"})
		require.NoError(t, err)
		require.Equal(t, 1, reader.reads)
		require.Equal(t, []string{"alice", "nobody", "alice"}, reader.ids)
		require.Equal(t, []ResolvedAgent{
			{Reference: "alice", Known: true, AgentAppearanceView: alice},
			{Reference: " nobody "},
			{Reference: ""},
			{Reference: "alice", Known: true, AgentAppearanceView: alice},
		}, results)
	}
}

func TestResolveAgentsRejectsUnboundedRequests(t *testing.T) {
	reader := &appearanceStub{}
	handler := NewResolveAgentsHandler(reader)
	_, err := handler.ByWork(t.Context(), strings.Split(strings.Repeat("w,", MaxResolvedAgents), ","))
	require.ErrorIs(t, err, ErrInvalidDefinitionSelection)
	require.Zero(t, reader.reads)
	reader.err = errors.New("offline")
	_, err = handler.ByEmployee(t.Context(), " ", []string{"alice"})
	require.ErrorIs(t, err, ErrInvalidDefinitionSelection, "the caller names the purpose")
	_, err = handler.ByEmployee(t.Context(), "work", []string{"alice"})
	require.ErrorIs(t, err, reader.err)
}

// Intent (real): an employee's own Work Agent reads as it is, or, before it
// exists, as the appearance it would be established with.
func TestOfEmployeeReadsTheAgentOrItsDefault(t *testing.T) {
	scout := AgentAppearanceView{AgentID: "agent-a", Appearance: domain.AgentAppearance{Name: "Scout", Icon: domain.DefaultAgentIcon}}
	reader := &appearanceStub{views: map[string]AgentAppearanceView{"alice": scout}}
	handler := NewResolveAgentsHandler(reader)
	view, err := handler.OfEmployee(t.Context(), "work", " alice ", "Alice")
	require.NoError(t, err)
	require.Equal(t, scout, view)
	require.Equal(t, "work", reader.purpose)
	view, err = handler.OfEmployee(t.Context(), "work", "bob", "Bob")
	require.NoError(t, err)
	require.Equal(t, AgentAppearanceView{Appearance: domain.AgentAppearance{Name: "Bob's Agent", Icon: domain.DefaultAgentIcon}}, view)
	_, err = handler.OfEmployee(t.Context(), "work", " ", "Nobody")
	require.ErrorIs(t, err, ErrInvalidDefinitionSelection)
	reader.err = errors.New("offline")
	_, err = handler.OfEmployee(t.Context(), "work", "alice", "Alice")
	require.ErrorIs(t, err, reader.err)
}
