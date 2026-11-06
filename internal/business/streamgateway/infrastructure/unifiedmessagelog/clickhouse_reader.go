package unifiedmessagelog

import (
	"context"
	"fmt"
	"strings"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/samber/oops"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
)

// messageTable is the Dispatcher-written UnifiedMessage log.
const messageTable = "talgent_messages.unified_message"

const selectColumns = `id, agent_work_id, turn_id, agent, kind, tool_call_id, interaction_id, payload_json, timestamp, detail_format, detail_length`

type ClickHouseReader struct {
	conn driver.Conn
}

func NewClickHouseReader(conn driver.Conn) query.UnifiedMessageReader {
	return &ClickHouseReader{conn: conn}
}

func (r *ClickHouseReader) ListReplay(ctx context.Context, sessionID string, cursor query.ReplayCursor) ([]*query.Message, error) {
	limit := cursor.Limit
	if limit <= 0 {
		limit = 500
	}
	latestReplay := cursor.Latest && cursor.After.IsZero() && cursor.AfterID == ""
	after := cursor.After
	if after.IsZero() {
		after = epochStart
	}
	where := "agent_work_id = {agent_work_id:String} AND timestamp > {after:DateTime64(3, 'UTC')}"
	params := clickhousego.Parameters{
		"agent_work_id": sessionID,
		"after":         after.UTC().Format("2006-01-02 15:04:05.000"),
	}
	if cursor.AfterID != "" {
		where = `agent_work_id = {agent_work_id:String} AND (
	timestamp > {after:DateTime64(3, 'UTC')}
	OR (timestamp = {after:DateTime64(3, 'UTC')} AND id > {after_id:String})
)`
		params["after_id"] = cursor.AfterID
	}
	if latestReplay {
		where = "agent_work_id = {agent_work_id:String}"
	}
	if filters := productFilters(params, cursor.Agent, cursor.Kinds, cursor.TurnIDs, cursor.IncludeUnattributedErrors); len(filters) > 0 {
		where += " AND " + strings.Join(filters, " AND ")
	}
	queryText := replayQueryText(latestReplay, where, limit)

	qctx := clickhousego.Context(ctx, clickhousego.WithParameters(params))
	rows, err := r.conn.Query(qctx, queryText)
	if err != nil {
		return nil, oops.With("agent_work_id", sessionID).Wrap(err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func replayQueryText(latestReplay bool, where string, limit int) string {
	if latestReplay {
		return fmt.Sprintf(`
SELECT `+selectColumns+`
FROM (
	SELECT `+selectColumns+`
	FROM `+messageTable+` FINAL
	WHERE `+where+`
	ORDER BY timestamp DESC, id DESC
	LIMIT %d
)
ORDER BY timestamp ASC, id ASC`, limit)
	}
	return fmt.Sprintf(`
SELECT `+selectColumns+`
FROM `+messageTable+` FINAL
WHERE `+where+`
ORDER BY timestamp ASC, id ASC
LIMIT %d`, limit)
}

func (r *ClickHouseReader) ListMessagesBySession(ctx context.Context, q query.ListMessagesBySessionRequest) ([]*query.Message, error) {
	toolCallIDs := normalizeFilterValues(q.ToolCallIDs)
	interactionIDs := normalizeFilterValues(q.InteractionIDs)
	limit := q.Limit
	if limit <= 0 || limit > 501 {
		limit = 500
	}
	params := clickhousego.Parameters{"agent_work_id": q.AgentWorkID}
	filters := make([]string, 0, 6)
	filters = append(filters, productFilters(params, q.Agent, q.Kinds, q.TurnIDs, q.IncludeUnattributedErrors)...)
	if len(toolCallIDs) > 0 {
		params["tool_call_ids"] = stringArrayParam(toolCallIDs)
		filters = append(filters, "has({tool_call_ids:Array(String)}, tool_call_id)")
	}
	if len(interactionIDs) > 0 {
		params["interaction_ids"] = stringArrayParam(interactionIDs)
		filters = append(filters, "has({interaction_ids:Array(String)}, interaction_id)")
	}
	boundary := ""
	if q.Before != nil {
		boundary = " AND (timestamp, id) < ({before:DateTime64(3, 'UTC')}, {before_id:String})"
		params["before"] = q.Before.Timestamp.UTC().Format("2006-01-02 15:04:05.000")
		params["before_id"] = q.Before.ID
	}
	queryText := listMessagesBySessionQueryText(strings.Join(filters, " AND "), limit, boundary)
	if q.From != nil {
		params["from"] = q.From.Timestamp.UTC().Format("2006-01-02 15:04:05.000")
		params["from_id"] = q.From.ID
		filters = append(filters, "(timestamp, id) >= ({from:DateTime64(3, 'UTC')}, {from_id:String})")
		queryText = replayQueryText(false, "agent_work_id = {agent_work_id:String} AND "+strings.Join(filters, " AND "), limit)
	}

	qctx := clickhousego.Context(ctx, clickhousego.WithParameters(params))
	rows, err := r.conn.Query(qctx, queryText)
	if err != nil {
		return nil, oops.With("agent_work_id", q.AgentWorkID).Wrap(err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func listMessagesBySessionQueryText(filterClause string, limit int, boundary string) string {
	filter := ""
	if clause := strings.TrimSpace(filterClause); clause != "" {
		filter = " AND (" + clause + ")"
	}
	return fmt.Sprintf(`
SELECT `+selectColumns+`
FROM (
	SELECT `+selectColumns+`
	FROM `+messageTable+` FINAL
	WHERE agent_work_id = {agent_work_id:String}`+filter+boundary+`
	ORDER BY timestamp DESC, id DESC
	LIMIT %d
)
ORDER BY timestamp ASC, id ASC`, limit)
}

type LazyClickHouseReader struct {
	client *pkgclickhouse.LazyConn
}

func NewLazyClickHouseReader(client *pkgclickhouse.LazyConn) query.UnifiedMessageReader {
	return &LazyClickHouseReader{client: client}
}

func (r *LazyClickHouseReader) ListReplay(ctx context.Context, sessionID string, cursor query.ReplayCursor) ([]*query.Message, error) {
	conn, err := r.client.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return NewClickHouseReader(conn).ListReplay(ctx, sessionID, cursor)
}

func (r *LazyClickHouseReader) ListMessagesBySession(ctx context.Context, q query.ListMessagesBySessionRequest) ([]*query.Message, error) {
	conn, err := r.client.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return NewClickHouseReader(conn).ListMessagesBySession(ctx, q)
}

var epochStart = time.Unix(0, 0).UTC()

var messageJSONUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

type messageDO struct {
	ID            string
	AgentWorkID   string
	TurnID        string
	Agent         *string
	Kind          string
	ToolCallID    string
	InteractionID string
	PayloadJSON   string
	Timestamp     time.Time
	DetailFormat  uint8
	DetailLength  uint32
}

func scanMessages(rows driver.Rows) ([]*query.Message, error) {
	var out []*query.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, oops.Wrap(err)
		}
		out = append(out, m)
	}
	return out, oops.Wrap(rows.Err())
}

func scanMessage(rows driver.Rows) (*query.Message, error) {
	var do messageDO
	if err := rows.Scan(
		&do.ID, &do.AgentWorkID, &do.TurnID, &do.Agent, &do.Kind,
		&do.ToolCallID, &do.InteractionID,
		&do.PayloadJSON, &do.Timestamp, &do.DetailFormat, &do.DetailLength,
	); err != nil {
		return nil, err
	}
	return doToMessage(&do)
}

func doToMessage(do *messageDO) (*query.Message, error) {
	msg := &dispatcherpublicv1.UnifiedMessage{}
	if do.PayloadJSON != "" {
		if err := messageJSONUnmarshal.Unmarshal([]byte(do.PayloadJSON), msg); err != nil {
			return nil, oops.Wrap(err)
		}
	}
	msg.Id = do.ID
	msg.AgentWorkId = do.AgentWorkID
	msg.TurnId, msg.Agent = do.TurnID, do.Agent
	msg.Timestamp = timestamppb.New(do.Timestamp)
	msg.DetailFormat = dispatcherpublicv1.MessageDetailFormat(do.DetailFormat)
	msg.DetailLength = do.DetailLength
	if msg.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED {
		msg.Kind = kindFromString(do.Kind)
	}
	return infrastructure.MessageFromProto(msg), nil
}

func kindFromString(s string) dispatcherpublicv1.UnifiedMessageKind {
	if v, ok := dispatcherpublicv1.UnifiedMessageKind_value[s]; ok {
		return dispatcherpublicv1.UnifiedMessageKind(v)
	}
	return dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED
}

func normalizeFilterValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func stringArrayParam(values []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('\'')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `'`, `\'`))
		b.WriteByte('\'')
	}
	b.WriteByte(']')
	return b.String()
}

func productFilters(params clickhousego.Parameters, agent *string, kinds, turns []string, includeUnattributedErrors bool) []string {
	var filters []string
	if agent != nil {
		params["agent"] = *agent
		predicate := "agent = {agent:String}"
		if includeUnattributedErrors {
			predicate = "(" + predicate + " OR (agent IS NULL AND kind = 'UNIFIED_MESSAGE_KIND_ERROR'))"
		}
		filters = append(filters, predicate)
	}
	if turns := normalizeFilterValues(turns); len(turns) > 0 {
		params["turn_ids"] = stringArrayParam(turns)
		filters = append(filters, "has({turn_ids:Array(String)}, turn_id)")
	}
	if kinds := normalizeFilterValues(kinds); len(kinds) > 0 {
		params["kinds"] = stringArrayParam(kinds)
		filters = append(filters, "has({kinds:Array(String)}, kind)")
	}
	return filters
}
