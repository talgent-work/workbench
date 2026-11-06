//go:build integration

package turnresults

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
)

type openConn struct{ conn driver.Conn }

func (c openConn) Conn(context.Context) (driver.Conn, error) { return c.conn, nil }

// Only the root stream's results of the requested Work are its turn results:
// a subagent's result and another Work's result are not.
func TestReaderListsRootTurnResultsInStreamOrder(t *testing.T) {
	dsn := middlewaretest.ClickHouse(t, "talgent_messages")
	options, err := clickhouse.ParseDSN(dsn)
	require.NoError(t, err)
	conn, err := clickhouse.Open(options)
	require.NoError(t, err)
	defer conn.Close()
	base := time.Now().UTC().Truncate(time.Millisecond)
	insert := func(id, work, agent, kind, payload string, at time.Time) {
		require.NoError(t, conn.Exec(context.Background(), `INSERT INTO talgent_messages.unified_message
			(id, agent_work_id, turn_id, agent, kind, tool_call_id, interaction_id, payload_json, timestamp, detail_format, detail_length, detail_body)
			VALUES (?, ?, 'turn', ?, ?, '', '', ?, ?, 0, 0, '')`, id, work, agent, kind, payload, at))
	}
	insert("r2", "work", "", "UNIFIED_MESSAGE_KIND_RESULT", `{"kind":"UNIFIED_MESSAGE_KIND_RESULT","result":{"status":"RESULT_STATUS_SUCCESS","text":"Final: use B"}}`, base.Add(2*time.Second))
	insert("r1", "work", "", "UNIFIED_MESSAGE_KIND_RESULT", `{"kind":"UNIFIED_MESSAGE_KIND_RESULT","result":{"status":"RESULT_STATUS_SUCCESS","text":"Tried A"}}`, base)
	insert("m4-sub", "work", "researcher", "UNIFIED_MESSAGE_KIND_RESULT", `{"kind":"UNIFIED_MESSAGE_KIND_RESULT","result":{"text":"subagent internal"}}`, base.Add(time.Second))
	insert("other", "other-work", "", "UNIFIED_MESSAGE_KIND_RESULT", `{"kind":"UNIFIED_MESSAGE_KIND_RESULT","result":{"text":"another Work"}}`, base)
	insert("text", "work", "", "UNIFIED_MESSAGE_KIND_ASSISTANT", `{"kind":"UNIFIED_MESSAGE_KIND_ASSISTANT"}`, base)

	reader := &Reader{conn: openConn{conn: conn}}
	var results []query.RootTurnResult
	require.Eventually(t, func() bool {
		results, err = reader.ListRootTurnResults(context.Background(), "work")
		return err == nil && len(results) == 2
	}, 5*time.Second, 50*time.Millisecond)
	require.Equal(t, "r1", results[0].MessageID)
	require.Equal(t, "Tried A", results[0].Text)
	require.Equal(t, "Final: use B", results[1].Text)
	require.True(t, results[1].OccurredAt.After(results[0].OccurredAt))
}

// A call's position is where its step returned in the Work's own root
// stream: the last result of the calls made beside it, once the Harness moved
// on. A step still waiting on a parallel call, a subagent's call and another
// Work's call have none.
func TestReaderFindsWhereACallReturned(t *testing.T) {
	dsn := middlewaretest.ClickHouse(t, "talgent_messages")
	options, err := clickhouse.ParseDSN(dsn)
	require.NoError(t, err)
	conn, err := clickhouse.Open(options)
	require.NoError(t, err)
	defer conn.Close()
	at := time.Now().UTC().Truncate(time.Millisecond)
	insert := func(id, work, agent, kind, call, payload string, offset time.Duration) {
		require.NoError(t, conn.Exec(context.Background(), `INSERT INTO talgent_messages.unified_message
			(id, agent_work_id, turn_id, agent, kind, tool_call_id, interaction_id, payload_json, timestamp, detail_format, detail_length, detail_body)
			VALUES (?, ?, 'turn', ?, ?, ?, '', ?, ?, 0, 0, '')`, id, work, agent, kind, call, payload, at.Add(offset)))
	}
	result := func(call, position string) string {
		return `{"kind":"UNIFIED_MESSAGE_KIND_OBSERVATION","observation":{"tool_call_id":"` + call + `","context_position":"` + position + `"}}`
	}
	insert("m1-call", "work", "", "UNIFIED_MESSAGE_KIND_ACTION", "toolu_invite", `{"kind":"UNIFIED_MESSAGE_KIND_ACTION"}`, 0)
	insert("m2-result", "work", "", "UNIFIED_MESSAGE_KIND_OBSERVATION", "toolu_invite", result("toolu_invite", "native-uuid-1"), time.Millisecond)
	insert("m3-sibling", "work", "", "UNIFIED_MESSAGE_KIND_OBSERVATION", "toolu_sibling", result("toolu_sibling", "native-uuid-2"), 2*time.Millisecond)
	insert("m4-sub", "work", "researcher", "UNIFIED_MESSAGE_KIND_OBSERVATION", "toolu_sub", result("toolu_sub", "native-uuid-9"), 3*time.Millisecond)
	insert("other", "other-work", "", "UNIFIED_MESSAGE_KIND_OBSERVATION", "toolu_other", result("toolu_other", "native-uuid-3"), 3*time.Millisecond)
	insert("waiting", "waiting-work", "", "UNIFIED_MESSAGE_KIND_OBSERVATION", "toolu_waiting", result("toolu_waiting", "native-uuid-4"), 3*time.Millisecond)

	reader := &Reader{conn: openConn{conn: conn}}
	// The step has not moved on yet: a parallel call may still be running.
	require.Eventually(t, func() bool {
		_, found, err := reader.FindCallPosition(context.Background(), "work", "toolu_invite")
		return err == nil && !found
	}, 5*time.Second, 50*time.Millisecond)
	insert("m5-next", "work", "", "UNIFIED_MESSAGE_KIND_ASSISTANT", "", `{"kind":"UNIFIED_MESSAGE_KIND_ASSISTANT"}`, 4*time.Millisecond)
	require.Eventually(t, func() bool {
		position, found, err := reader.FindCallPosition(context.Background(), "work", "toolu_invite")
		return err == nil && found && position == "native-uuid-2"
	}, 5*time.Second, 50*time.Millisecond, "the position follows every result of the step")
	for _, call := range [][2]string{{"work", "toolu_pending"}, {"work", "toolu_sub"}, {"other-work", "toolu_other"}, {"waiting-work", "toolu_waiting"}} {
		_, found, err := reader.FindCallPosition(context.Background(), call[0], call[1])
		require.NoError(t, err)
		require.False(t, found, call[1])
	}
}
