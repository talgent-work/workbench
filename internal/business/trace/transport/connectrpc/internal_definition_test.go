package connectrpc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestProductTraceOmitsInternalDefinitionIdentity(t *testing.T) {
	span := sampleOwnerSpan(time.Now())
	span.AgentID = "private-definition-identity"
	span.Attributes["talgent.agent_id"] = span.AgentID
	encoded, err := protojson.Marshal(spanToProto(span))
	require.NoError(t, err)
	require.NotContains(t, string(encoded), span.AgentID)
	require.NotContains(t, string(encoded), "agentId")
	require.NotContains(t, string(encoded), "talgent.agent_id")
	require.NotContains(t, string(encoded), "executionId", "product traces are attributed per Work")
	require.Contains(t, string(encoded), span.RuntimePurpose)
	require.Equal(t, span.AgentID, span.Attributes["talgent.agent_id"], "public projection must not erase internal telemetry attribution")
}
