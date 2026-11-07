//go:build integration

package unifiedmessagelog_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"
	sgquery "github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	sginfra "github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure"
	sglog "github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure/unifiedmessagelog"
	"google.golang.org/protobuf/types/known/timestamppb"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/unifiedmessagelog"
)

func startClickHouse(t *testing.T) (context.Context, string, func()) {
	t.Helper()
	return t.Context(), middlewaretest.ClickHouse(t, "talgent_messages"), func() {}
}

// Equal message IDs in two Works remain separate in history and pagination,
// and a message written again, as a stream replay does, is read once.
func TestWorkHistoryIsIsolatedAndRepeatedWritesAreReadOnce(t *testing.T) {
	ctx, dsn, cleanup := startClickHouse(t)
	defer cleanup()
	opts, err := clickhouse.ParseDSN(dsn)
	require.NoError(t, err)
	conn, err := clickhouse.Open(opts)
	require.NoError(t, err)
	defer conn.Close()
	writer := unifiedmessagelog.NewWriter(conn)
	reader := sglog.NewClickHouseReader(conn)
	now := time.Now().UTC().Truncate(time.Millisecond)
	works := []string{"work-agent", "project-agent"}
	originals := make(map[string][]*dispatcherpublicv1.UnifiedMessage)
	for _, workID := range works {
		for index, id := range []string{"message-1", "message-2"} {
			message := textMessage(id, workID, now)
			message.TurnId = "turn-" + workID
			agent := ""
			if index == 1 {
				agent = "native-child"
			}
			message.Agent = &agent
			originals[workID] = append(originals[workID], message)
			require.NoError(t, writer.Append(ctx, message))
		}
		// A replay repeats the same identities.
		require.NoError(t, writer.Append(ctx, originals[workID]...))
	}
	// Visibility remains asynchronous; wait until every identity is persisted.
	require.Eventually(t, func() bool {
		var identities uint64
		err := conn.QueryRow(ctx, "SELECT count() FROM "+unifiedmessagelog.Table+" FINAL").Scan(&identities)
		return err == nil && identities == 4
	}, 10*time.Second, 50*time.Millisecond)
	for _, workID := range works {
		history, err := reader.ListMessagesBySession(ctx, sgquery.ListMessagesBySessionRequest{AgentWorkID: workID, Limit: 10})
		require.NoError(t, err)
		require.Len(t, history, 2)
		for i, message := range history {
			require.Equal(t, sginfra.MessageFromProto(originals[workID][i]), message, "payload and attribution must survive the Work-scoped log")
		}
		first, err := reader.ListReplay(ctx, workID, sgquery.ReplayCursor{Limit: 1})
		require.NoError(t, err)
		require.Len(t, first, 1)
		require.Equal(t, "message-1", first[0].ID)
		next, err := reader.ListReplay(ctx, workID, sgquery.ReplayCursor{After: now, AfterID: first[0].ID, Limit: 1})
		require.NoError(t, err)
		require.Len(t, next, 1)
		require.Equal(t, "message-2", next[0].ID)
		require.Equal(t, workID, next[0].AgentWorkID)
		latest, err := reader.ListReplay(ctx, workID, sgquery.ReplayCursor{Latest: true, Limit: 1})
		require.NoError(t, err)
		require.Len(t, latest, 1)
		require.Equal(t, workID, latest[0].AgentWorkID)
		require.Equal(t, "message-2", latest[0].ID)
	}
}

func textMessage(id, sessionID string, ts time.Time) *dispatcherpublicv1.UnifiedMessage {
	return &dispatcherpublicv1.UnifiedMessage{Id: id, AgentWorkId: sessionID, Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT, Timestamp: timestamppb.New(ts), Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{Content: []*dispatcherpublicv1.ContentBlock{{Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT, Text: "text"}}}}}
}

// Fixed queryable history must be covered exactly once even with timestamp ties,
// subsequent live inserts, and execution changes within the same Session.
func TestBackwardHistoryCoversStoredSessionAcrossExecutions(t *testing.T) {
	ctx, dsn, cleanup := startClickHouse(t)
	defer cleanup()
	opts, err := clickhouse.ParseDSN(dsn)
	require.NoError(t, err)
	conn, err := clickhouse.Open(opts)
	require.NoError(t, err)
	defer conn.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	limits := []int{100, 41, 500}
	// Each page size reads its own copy of the history, so the live insert
	// made while paging one copy never reaches another.
	session := func(limit int) string { return fmt.Sprintf("session-%d", limit) }
	batch, err := conn.PrepareBatch(ctx, `INSERT INTO talgent_messages.unified_message (id, agent_work_id, timestamp, turn_id)`)
	require.NoError(t, err)
	for _, limit := range limits {
		for i := 1; i <= 205; i++ {
			require.NoError(t, batch.Append(fmt.Sprintf("m%03d", i), session(limit), now.Add(time.Duration(i/10)*time.Millisecond), fmt.Sprintf("turn-%d", i/100)))
		}
	}
	require.NoError(t, batch.Send())
	handler := sgquery.NewListMessagesBySessionHandler(sglog.NewClickHouseReader(conn), allowedWorkRead{})
	for _, limit := range limits {
		req := sgquery.ListMessagesBySessionRequest{AgentWorkID: session(limit), Limit: limit}
		seen := map[string]bool{}
		var pages int
		for {
			page, err := handler.Handle(ctx, req)
			require.NoError(t, err)
			require.NotEmpty(t, page.Messages)
			for _, message := range page.Messages {
				require.False(t, seen[message.ID], "duplicate %s", message.ID)
				seen[message.ID] = true
				require.NotEmpty(t, message.TurnID)
			}
			pages++
			if pages == 1 && limit == 100 {
				require.NoError(t, conn.Exec(ctx, `INSERT INTO talgent_messages.unified_message (id, agent_work_id, timestamp) VALUES ('live', ?, ?)`, session(limit), now.Add(time.Hour)))
			}
			if !page.HasMore {
				break
			}
			require.NotNil(t, page.NextBefore)
			req.Before = page.NextBefore
			require.Less(t, pages, 10)
		}
		for i := 1; i <= 205; i++ {
			require.True(t, seen[fmt.Sprintf("m%03d", i)])
		}
		require.Equal(t, 205, len(seen))
	}
	empty, err := handler.Handle(ctx, sgquery.ListMessagesBySessionRequest{AgentWorkID: "empty", Limit: 100})
	require.NoError(t, err)
	require.Empty(t, empty.Messages)
	require.False(t, empty.HasMore)
	require.Nil(t, empty.NextBefore)
}

func TestResultHistoryFiltersBeforePagingPastToolTraffic(t *testing.T) {
	ctx, dsn, cleanup := startClickHouse(t)
	defer cleanup()
	opts, err := clickhouse.ParseDSN(dsn)
	require.NoError(t, err)
	conn, err := clickhouse.Open(opts)
	require.NoError(t, err)
	defer conn.Close()
	ts := time.Now().UTC().Truncate(time.Millisecond)
	batch, err := conn.PrepareBatch(ctx, `INSERT INTO talgent_messages.unified_message (id, agent_work_id, kind, timestamp, payload_json, turn_id, agent)`)
	require.NoError(t, err)
	for _, id := range []string{"result-a", "result-b", "result-c"} {
		require.NoError(t, batch.Append(id, "session", "UNIFIED_MESSAGE_KIND_RESULT", ts, `{"result":{"status":"RESULT_STATUS_FAILED","text":"Original result"}}`, "turn-one", ""))
	}
	for i := range 150 {
		require.NoError(t, batch.Append(fmt.Sprintf("tool-%03d", i), "session", "UNIFIED_MESSAGE_KIND_ACTION", ts.Add(time.Second), `{"action":{"toolCallId":"tool","toolName":"Read"}}`, "turn-one", ""))
	}
	require.NoError(t, batch.Append("other-session-result", "other", "UNIFIED_MESSAGE_KIND_RESULT", ts.Add(time.Hour), `{"result":{"text":"Other session"}}`, "turn-one", ""))
	require.NoError(t, batch.Append("child-result", "session", "UNIFIED_MESSAGE_KIND_RESULT", ts.Add(time.Hour), `{"result":{"text":"Child result"}}`, "turn-one", "native-child"))
	require.NoError(t, batch.Append("unknown-result", "session", "UNIFIED_MESSAGE_KIND_RESULT", ts.Add(time.Hour), `{"result":{"text":"Unknown owner"}}`, "turn-one", nil))
	require.NoError(t, batch.Append("other-turn", "session", "UNIFIED_MESSAGE_KIND_RESULT", ts.Add(time.Hour), `{"result":{"text":"Next result"}}`, "turn-two", ""))
	for _, row := range []struct {
		id    string
		agent any
		turn  string
	}{
		{"root-error", "", "turn-one"},
		{"child-error", "native-child", "turn-one"},
		{"unknown-error", nil, ""},
	} {
		require.NoError(t, batch.Append(row.id, "session", "UNIFIED_MESSAGE_KIND_ERROR", ts.Add(2*time.Hour), `{"error":{"code":"api_error","message":"Original provider failure"}}`, row.turn, row.agent))
	}

	require.NoError(t, batch.Send())
	handler := sgquery.NewListMessagesBySessionHandler(sglog.NewClickHouseReader(conn), allowedWorkRead{})
	root := ""
	request := sgquery.ListMessagesBySessionRequest{AgentWorkID: "session", Agent: &root, TurnIDs: []string{"turn-one"}, Kinds: []string{"UNIFIED_MESSAGE_KIND_RESULT"}, Limit: 2}
	page, err := handler.Handle(ctx, request)
	require.NoError(t, err)
	require.Len(t, page.Messages, 2)
	require.Equal(t, "result-b", page.Messages[0].ID)
	require.Equal(t, "result-c", page.Messages[1].ID)
	require.EqualValues(t, dispatcherpublicv1.ResultStatus_RESULT_STATUS_FAILED, page.Messages[0].Result.Status)
	require.True(t, page.HasMore)
	require.Equal(t, "result-b", page.NextBefore.ID)
	forward := request
	forward.From = &sgquery.MessageHistoryCursor{Timestamp: ts, ID: "result-a"}
	ahead, err := handler.Handle(ctx, forward)
	require.NoError(t, err)
	require.Len(t, ahead.Messages, 2)
	require.Equal(t, "result-a", ahead.Messages[0].ID)
	require.Equal(t, "result-b", ahead.Messages[1].ID)
	require.True(t, ahead.HasMore)
	forward.From.ID = "result-c"
	ahead, err = handler.Handle(ctx, forward)
	require.NoError(t, err)
	require.Len(t, ahead.Messages, 1)
	require.Equal(t, "result-c", ahead.Messages[0].ID)
	require.False(t, ahead.HasMore)
	request.Before = page.NextBefore
	page, err = handler.Handle(ctx, request)
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.Equal(t, "result-a", page.Messages[0].ID)
	require.False(t, page.HasMore)
	reader := sglog.NewClickHouseReader(conn)
	replay, err := reader.ListReplay(ctx, "session", sgquery.ReplayCursor{Latest: true, Limit: 2, Agent: &root, Kinds: request.Kinds, TurnIDs: request.TurnIDs})
	require.NoError(t, err)
	require.Len(t, replay, 2)
	require.Equal(t, "result-b", replay[0].ID)
	require.Equal(t, "result-c", replay[1].ID)
	replay, err = reader.ListReplay(ctx, "session", sgquery.ReplayCursor{After: ts, AfterID: "result-b", Limit: 2, Agent: &root, Kinds: request.Kinds, TurnIDs: request.TurnIDs})
	require.NoError(t, err)
	require.Len(t, replay, 1)
	require.Equal(t, "result-c", replay[0].ID)

	// Error inclusion relaxes only Agent attribution. It applies before paging,
	// and never pulls child errors or unknown Results into the root stream.
	request = sgquery.ListMessagesBySessionRequest{AgentWorkID: "session", Agent: &root, Kinds: []string{"UNIFIED_MESSAGE_KIND_ERROR"}, Limit: 1, IncludeUnattributedErrors: true}
	page, err = handler.Handle(ctx, request)
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.Equal(t, "unknown-error", page.Messages[0].ID)
	require.Nil(t, page.Messages[0].Agent)
	require.Equal(t, "Original provider failure", page.Messages[0].Error.Message)
	require.True(t, page.HasMore)
	request.Before = page.NextBefore
	page, err = handler.Handle(ctx, request)
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.Equal(t, "root-error", page.Messages[0].ID)
	require.False(t, page.HasMore)
	replay, err = reader.ListReplay(ctx, "session", sgquery.ReplayCursor{After: ts.Add(2 * time.Hour), AfterID: "root-error", Limit: 2, Agent: &root, Kinds: request.Kinds, IncludeUnattributedErrors: true})
	require.NoError(t, err)
	require.Len(t, replay, 1)
	require.Equal(t, "unknown-error", replay[0].ID)
	require.Nil(t, replay[0].Agent)
	request.Before, request.IncludeUnattributedErrors = nil, false
	page, err = handler.Handle(ctx, request)
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.Equal(t, "root-error", page.Messages[0].ID)
	require.False(t, page.HasMore)
	request.IncludeUnattributedErrors, request.TurnIDs = true, []string{"turn-one"}
	page, err = handler.Handle(ctx, request)
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.Equal(t, "root-error", page.Messages[0].ID)
	require.False(t, page.HasMore)
}

type allowedWorkRead struct{}

func (allowedWorkRead) AuthorizeRead(context.Context, string, sgquery.ReaderAdmission, string) error {
	return nil
}
