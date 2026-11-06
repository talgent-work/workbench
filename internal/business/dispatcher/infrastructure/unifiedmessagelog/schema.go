package unifiedmessagelog

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

const (
	Database = "talgent_messages"
	Table    = Database + ".unified_message"
)

const createDBStmt = `CREATE DATABASE IF NOT EXISTS ` + Database

// A replayed Harness Output Stream writes the same message identity again;
// the latest write replaces earlier ones.
const createTableStmt = `
CREATE TABLE IF NOT EXISTS ` + Table + ` (
    id                String,
    agent_work_id     String,
    turn_id           String,
    agent             Nullable(String),
    kind              LowCardinality(String),
    tool_call_id      String,
    interaction_id    String,
    payload_json      String,
    detail_format     UInt8,
    detail_length     UInt32,
    detail_body       String,
    tool_preview      String DEFAULT '',
    timestamp         DateTime64(3, 'UTC'),
    written_at        DateTime64(3, 'UTC') DEFAULT now64(3),
    INDEX idx_tool_call_id tool_call_id TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_interaction_id interaction_id TYPE bloom_filter(0.01) GRANULARITY 4
) ENGINE = ReplacingMergeTree(written_at)
  ORDER BY (agent_work_id, id)
  SETTINGS index_granularity = 8192
`

// ApplySchema creates the message log.
func ApplySchema(ctx context.Context, conn driver.Conn) error {
	if err := conn.Exec(ctx, createDBStmt); err != nil {
		return err
	}
	return conn.Exec(ctx, createTableStmt)
}
