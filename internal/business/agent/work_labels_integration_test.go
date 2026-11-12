//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
)

// An Application describes each Work with its own Work Labels at start: the
// Work keeps them, its runs' Identity Token states them, and its listing
// shows them and narrows by them and by Work State. Malformed labels start
// nothing.
func TestAWorkCarriesTheWorkLabelsItsStarterGaveIt(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk := m.as("application", supportDesk)
	start := func(labels map[string]string) (string, error) {
		response, err := desk.works.StartWork(desk.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Answer the ticket", Labels: labels})
		return response.GetWorkId(), err
	}
	list := func(request *agentpublicv2.ListWorksRequest) map[string]map[string]string {
		t.Helper()
		response, err := desk.works.ListWorks(desk.ctx, request)
		require.NoError(t, err)
		listed := map[string]map[string]string{}
		for _, work := range response.GetWorks() {
			listed[work.GetWorkId()] = work.GetLabels()
		}
		return listed
	}

	acme := map[string]string{"user": "u123", "tenant": "acme"}
	first, err := start(acme)
	require.NoError(t, err)
	second, err := start(map[string]string{"user": "u456", "tenant": "acme"})
	require.NoError(t, err)
	unlabelled, err := start(nil)
	require.NoError(t, err)

	stored, err := m.app.Works.Get(m.ctx, first)
	require.NoError(t, err)
	require.Equal(t, acme, stored.WorkLabels.Map(), "the Work keeps its labels")
	m.runs.ready(t, m.ctx, first)
	require.Equal(t, acme, m.runs.latest(t, m.ctx, first).Launch.WorkLabels, "its Identity Token states them")

	require.Equal(t, map[string]map[string]string{first: acme, second: {"user": "u456", "tenant": "acme"}, unlabelled: nil}, list(&agentpublicv2.ListWorksRequest{}))
	require.Equal(t, map[string]map[string]string{first: acme}, list(&agentpublicv2.ListWorksRequest{Labels: map[string]string{"user": "u123"}}), "one user's Works")
	require.Len(t, list(&agentpublicv2.ListWorksRequest{Labels: map[string]string{"tenant": "acme"}}), 2, "one tenant's Works")
	require.Empty(t, list(&agentpublicv2.ListWorksRequest{Labels: map[string]string{"tenant": "acme", "user": "u789"}}), "every label must match")
	require.Empty(t, list(&agentpublicv2.ListWorksRequest{Labels: map[string]string{"tenant": "ACME"}}), "values match exactly")
	require.Len(t, list(&agentpublicv2.ListWorksRequest{States: []string{"working"}}), 1, "the one ready Work")
	require.Len(t, list(&agentpublicv2.ListWorksRequest{States: []string{"starting", "working"}}), 3)
	require.Empty(t, list(&agentpublicv2.ListWorksRequest{States: []string{"closed"}}))
	require.Equal(t, map[string]map[string]string{first: acme}, list(&agentpublicv2.ListWorksRequest{Labels: map[string]string{"tenant": "acme"}, States: []string{"working"}}))

	_, err = desk.works.ListWorks(desk.ctx, &agentpublicv2.ListWorksRequest{States: []string{"sleeping"}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "an unknown state")
	for name, labels := range map[string]map[string]string{
		"key with a space": {"end user": "u1"},
		"empty value":      {"user": ""},
		"multi-line value": {"user": "u1\nu2"},
	} {
		_, err := start(labels)
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name)
	}
	require.Len(t, list(&agentpublicv2.ListWorksRequest{}), 3, "a refused start leaves no Work")
}
