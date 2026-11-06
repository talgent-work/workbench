package unifiedmessagelog

import (
	"strings"
	"testing"
	"time"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

// The Streamgateway reader must project only UnifiedMessage columns so replay
// cannot depend on removed runtime/raw/correlation sidecars.
func TestSelectColumnsOmitTheDetailBody(t *testing.T) {
	oldRuntimeRawColumn := "runtime" + "_raw" + "_json"
	oldCorrelationColumn := "correlation" + "_json"
	for _, column := range []string{
		"runtime_kind",
		oldRuntimeRawColumn,
		oldCorrelationColumn,
		"source_event_id",
		"operation_id",
		"tool_use_id",
		"parent_tool_use_id",
	} {
		if strings.Contains(selectColumns, column) {
			t.Fatalf("selectColumns contains legacy column %q: %s", column, selectColumns)
		}
	}
	for _, column := range []string{
		"id",
		"agent_work_id",
		"kind",
		"tool_call_id",
		"interaction_id",
		"payload_json",
		"timestamp",
	} {
		if !strings.Contains(selectColumns, column) {
			t.Fatalf("selectColumns missing product column %q: %s", column, selectColumns)
		}
	}
}

// Initial WatchStream hydration should read the tail of durable history while
// preserving chronological display order for Portal.
func TestReplayQueryLatestReadsTailThenRestoresChronologicalOrder(t *testing.T) {
	queryText := replayQueryText(true, "agent_work_id = {agent_work_id:String}", 50)
	for _, want := range []string{
		"FROM (",
		"ORDER BY timestamp DESC, id DESC",
		"LIMIT 50",
		")\nORDER BY timestamp ASC, id ASC",
	} {
		if !strings.Contains(queryText, want) {
			t.Fatalf("latest replay query missing %q:\n%s", want, queryText)
		}
	}
}

// Archive ingest asks for recent Work history by agent_work_id only; the reader must
// read the tail of the transcript and restore chronological order for the LLM.
func TestListMessagesBySessionQueryWithoutFiltersReadsRecentHistory(t *testing.T) {
	queryText := listMessagesBySessionQueryText("", 120, "")
	for _, want := range []string{
		"FROM (",
		"WHERE agent_work_id = {agent_work_id:String}",
		"ORDER BY timestamp DESC, id DESC",
		"LIMIT 120",
		")\nORDER BY timestamp ASC, id ASC",
	} {
		if !strings.Contains(queryText, want) {
			t.Fatalf("work-only query missing %q:\n%s", want, queryText)
		}
	}
	if strings.Contains(queryText, "AND ()") {
		t.Fatalf("work-only query contains empty filter clause:\n%s", queryText)
	}
}

// Filtered lookups keep the existing behavior for callers that need a specific
// tool/request/interaction context.
func TestListMessagesBySessionQueryWithFiltersAppliesFilterClause(t *testing.T) {
	queryText := listMessagesBySessionQueryText("has({tool_call_ids:Array(String)}, tool_call_id)", 50, "")
	for _, want := range []string{
		"WHERE agent_work_id = {agent_work_id:String}",
		"AND (has({tool_call_ids:Array(String)}, tool_call_id))",
		"ORDER BY timestamp ASC, id ASC",
		"LIMIT 50",
	} {
		if !strings.Contains(queryText, want) {
			t.Fatalf("filtered query missing %q:\n%s", want, queryText)
		}
	}
}

// Replay reconstruction must preserve product tree fields from the durable log,
// because Portal renders hierarchy from parent_message_id and relation.
func TestDoToMessagePreservesProductHierarchy(t *testing.T) {
	msg, err := doToMessage(&messageDO{
		ID:          "m1",
		AgentWorkID: "w1",
		Kind:        dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION.String(),
		PayloadJSON: `{"observation":{"tool_call_id":"tool-1"}}`,
		Timestamp:   time.Unix(10, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("doToMessage: %v", err)
	}
	if msg.Observation.ToolCallID != "tool-1" {
		t.Fatalf("tool_call_id = %q, want tool-1", msg.Observation.ToolCallID)
	}
}
