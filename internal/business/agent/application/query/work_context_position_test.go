package query_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type originsFake map[string]query.WorkOrigin

func (f originsFake) GetWorkOrigin(_ context.Context, workID string) (query.WorkOrigin, error) {
	origin, ok := f[workID]
	if !ok {
		return query.WorkOrigin{}, domain.ErrWorkNotFound
	}
	return origin, nil
}

func (f originsFake) ListWorkOrigins(_ context.Context, workIDs []string) (map[string]query.WorkOrigin, error) {
	out := map[string]query.WorkOrigin{}
	for _, id := range workIDs {
		if origin, ok := f[id]; ok {
			out[id] = origin
		}
	}
	return out, nil
}

type callPositions map[string]string

func (p callPositions) FindCallPosition(_ context.Context, workID, callID string) (string, bool, error) {
	position, ok := p[workID+"/"+callID]
	return position, ok, nil
}

// A call's position is named only once its result is recorded, and only for
// a Work of the asking Project; anything else is reported as not returned.
func TestWorkContextPositionNamesWhereACallReturned(t *testing.T) {
	handler := query.NewResolveWorkContextPositionHandler(
		originsFake{"project-work": {WorkID: "project-work", ProjectID: "project-a"}},
		callPositions{"project-work/toolu_invite": "native-uuid-1"},
	)
	position, err := handler.Handle(t.Context(), "project-a", "project-work", "toolu_invite")
	require.NoError(t, err)
	require.Equal(t, query.WorkContextPosition{Returned: true, Position: "native-uuid-1"}, position)

	for name, ask := range map[string][3]string{
		"call still running": {"project-a", "project-work", "toolu_pending"},
		"another Project":    {"project-b", "project-work", "toolu_invite"},
		"unknown Work":       {"project-a", "missing", "toolu_invite"},
	} {
		position, err := handler.Handle(t.Context(), ask[0], ask[1], ask[2])
		require.NoError(t, err, name)
		require.False(t, position.Returned, name)
		require.Empty(t, position.Position, name)
	}
	_, err = handler.Handle(t.Context(), "project-a", "project-work", " ")
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}
