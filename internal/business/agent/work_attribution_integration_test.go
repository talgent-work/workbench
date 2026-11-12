//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
)

// Agent tells a trusted context the Work Labels of the named Works an
// Application owns, and leaves out another Application's Works, a person's
// Works and unknown ones, so the context can narrow its own records by labels
// it keeps no copy of.
func TestAgentAttributesOnlyAnApplicationsOwnWorks(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk, bot, person := m.as("application", supportDesk), m.as("application", billingBot), m.as("user", uuid.NewString())
	start := func(owner ownerClients, labels map[string]string) string {
		t.Helper()
		response, err := owner.works.StartWork(owner.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Answer the ticket", Labels: labels})
		require.NoError(t, err)
		return response.GetWorkId()
	}
	labelled := start(desk, map[string]string{"tenant": "acme", "user": "u1"})
	unlabelled := start(desk, nil)
	others := start(bot, map[string]string{"tenant": "acme"})
	personal := start(person, map[string]string{"tenant": "acme"})

	client := agentprivatev1connect.NewWorkAttributionServiceClient(connect.NewClient(connecthttp.NewTransport(m.public, m.url, connecthttp.WithReadMaxBytes(0))))
	response, err := client.ListWorkAttributions(sessionControlRequest(m.ctx), &agentprivatev1.ListWorkAttributionsRequest{ApplicationId: supportDesk, WorkIds: []string{labelled, unlabelled, others, personal, uuid.NewString()}})
	require.NoError(t, err)
	attributed := map[string]map[string]string{}
	for _, work := range response.GetWorks() {
		attributed[work.GetWorkId()] = work.GetLabels()
	}
	require.Equal(t, map[string]map[string]string{labelled: {"tenant": "acme", "user": "u1"}, unlabelled: nil}, attributed)

	response, err = client.ListWorkAttributions(sessionControlRequest(m.ctx), &agentprivatev1.ListWorkAttributionsRequest{ApplicationId: uuid.NewString(), WorkIds: []string{labelled}})
	require.NoError(t, err)
	require.Empty(t, response.GetWorks(), "an Application without an Agent owns no Work")

	_, err = client.ListWorkAttributions(m.ctx, &agentprivatev1.ListWorkAttributionsRequest{ApplicationId: supportDesk, WorkIds: []string{labelled}})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "only a trusted caller asks")
	tooMany := make([]string, 501)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	_, err = client.ListWorkAttributions(sessionControlRequest(m.ctx), &agentprivatev1.ListWorkAttributionsRequest{ApplicationId: supportDesk, WorkIds: tooMany})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
