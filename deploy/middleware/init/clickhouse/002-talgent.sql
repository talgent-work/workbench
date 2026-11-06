-- Run after the official Collector creates its native tables. This file is
-- intentionally not mounted into ClickHouse's first-start init directory.
SELECT throwIf(countIf(database = 'talgent_trace' AND name IN ('otel_traces', 'otel_logs')) != 2,
    'Start the configured Collector before applying native observability projections') FROM system.tables;

-- Native evidence is written by the official Collector (7-day TTL).
-- Measurement facts contain no input/output body and expire after 30 days.
-- Service accounts are provisioned by operators outside this schema migration
-- (see docs/self-hosting.md), and XML-defined users reject SQL GRANT statements.
CREATE DATABASE IF NOT EXISTS talgent_messages;

CREATE OR REPLACE FUNCTION talgent_log_evidence_id AS (trace_id, event_time, body, attrs) ->
    concat('log:', hex(SHA256(concat(trace_id, ':', attrs['session.id'], ':',
        attrs['event.sequence'], ':', toString(event_time), ':', body, ':',
        toString(arraySort(x -> x.1, arrayZip(mapKeys(attrs), mapValues(attrs))))))));

CREATE OR REPLACE FUNCTION talgent_call_kind AS (tool, server) ->
    multiIf(tool = 'Skill', 'skill_call', tool IN ('Agent', 'Task'), 'agent_call',
            tool = 'mcp_tool' OR server != '' OR startsWith(tool, 'mcp__'), 'mcp_call', tool != '', 'tool_call', 'unclassified_call');
CREATE OR REPLACE FUNCTION talgent_span_category AS (name, attrs) ->
    multiIf(name = 'claude_code.tool', talgent_call_kind(attrs['tool_name'], attrs['mcp_server_name']),
        name = 'claude_code.llm_request', 'model_call', name = 'claude_code.interaction', 'interaction',
        name = 'claude_code.tool.execution', 'execution', name = 'claude_code.tool.blocked_on_user', 'permission', 'other');
CREATE OR REPLACE FUNCTION talgent_call_dimension AS (tool, attrs) ->
    multiIf(tool = 'Skill', if(attrs['skill_name'] != '', attrs['skill_name'], 'Unknown skill'),
            tool IN ('Agent', 'Task'), if(attrs['subagent_type'] != '', attrs['subagent_type'], 'Unknown agent type'),
            tool = 'mcp_tool' AND attrs['mcp_server_name'] != '' AND attrs['mcp_tool_name'] != '', concat('mcp__', attrs['mcp_server_name'], '__', attrs['mcp_tool_name']),
            tool != '', tool, 'Unknown tool');
-- Token usage that is not recorded, such as on a rejected model request, or
-- is invalid counts as zero and never makes the usage unknown.
CREATE OR REPLACE FUNCTION talgent_usage AS (value) ->
    if(isFinite(toFloat64OrNull(value)) AND toFloat64OrNull(value) >= 0, toFloat64OrNull(value), 0);
CREATE OR REPLACE FUNCTION talgent_span_measurements AS (name, attrs) ->
    multiIf(name = 'claude_code.tool',
        [(talgent_call_kind(attrs['tool_name'], attrs['mcp_server_name']), CAST(1 AS Nullable(Float64)))],
        name = 'claude_code.llm_request',
        [('model_call', CAST(1 AS Nullable(Float64))),
         ('input_tokens', talgent_usage(attrs['input_tokens'])),
         ('output_tokens', talgent_usage(attrs['output_tokens'])),
         ('cache_read_tokens', talgent_usage(attrs['cache_read_tokens'])),
         ('cache_creation_tokens', talgent_usage(attrs['cache_creation_tokens']))],
        CAST([], 'Array(Tuple(String, Nullable(Float64)))'));

-- Measurements are attributed per Work, never per Harness process run, and
-- cover every Work, with or without a Project (an empty project_id).
CREATE TABLE IF NOT EXISTS talgent_trace.trace_work_measurements (
    evidence_id String,
    measurement_id String,
    timestamp DateTime64(9, 'UTC'),
    project_id String,
    agent_work_id String,
    agent_id String,
    runtime_purpose String,
    owner_context String,
    owner_id String,
    user_id String,
    dimension String,
    measurements Array(Tuple(String, Nullable(Float64))),
    gaps Array(String),
    source_priority UInt8,
    projection_version UInt64,
    -- A person's usage spans every Project, outside the sort key.
    INDEX idx_user_id user_id TYPE bloom_filter GRANULARITY 4
) ENGINE = ReplacingMergeTree(projection_version)
PARTITION BY toYYYYMM(timestamp)
ORDER BY (project_id, agent_work_id, evidence_id)
TTL toDateTime(timestamp) + INTERVAL 30 DAY;

-- Independent receipts contain only attribution and identity. Version zero is
-- replaced by a successful interpretation of the same evidence.
-- If an interpreter fails, the receipt exposes a gap without blocking raw storage.
-- This detects failures after receipt, not loss before collection or receipt failure.
DROP VIEW IF EXISTS talgent_trace.trace_spans_receipts_mv;
CREATE MATERIALIZED VIEW talgent_trace.trace_spans_receipts_mv
TO talgent_trace.trace_work_measurements AS
SELECT
    concat('span:', TraceId, ':', SpanId) AS evidence_id,
    evidence_id AS measurement_id,
    Timestamp AS timestamp,
    ResourceAttributes['talgent.project_id'] AS project_id,
    ResourceAttributes['talgent.agent_work_id'] AS agent_work_id,
    ResourceAttributes['talgent.agent_id'] AS agent_id,
    ResourceAttributes['talgent.runtime_purpose'] AS runtime_purpose,
    ResourceAttributes['talgent.owner_context'] AS owner_context,
    ResourceAttributes['talgent.owner_id'] AS owner_id,
    ResourceAttributes['talgent.user_id'] AS user_id,
    '' AS dimension,
    [('observation_gap', CAST(NULL AS Nullable(Float64)))] AS measurements,
    ['projection_incomplete'] AS gaps,
    toUInt8(0) AS source_priority,
    toUInt64(0) AS projection_version
FROM talgent_trace.otel_traces
WHERE agent_work_id != '';

DROP VIEW IF EXISTS talgent_trace.trace_logs_receipts_mv;
CREATE MATERIALIZED VIEW talgent_trace.trace_logs_receipts_mv
TO talgent_trace.trace_work_measurements AS
SELECT
    talgent_log_evidence_id(TraceId, Timestamp, Body, LogAttributes) AS evidence_id,
    evidence_id AS measurement_id,
    Timestamp AS timestamp,
    ResourceAttributes['talgent.project_id'] AS project_id,
    ResourceAttributes['talgent.agent_work_id'] AS agent_work_id,
    ResourceAttributes['talgent.agent_id'] AS agent_id,
    ResourceAttributes['talgent.runtime_purpose'] AS runtime_purpose,
    ResourceAttributes['talgent.owner_context'] AS owner_context,
    ResourceAttributes['talgent.owner_id'] AS owner_id,
    ResourceAttributes['talgent.user_id'] AS user_id,
    '' AS dimension,
    [('observation_gap', CAST(NULL AS Nullable(Float64)))] AS measurements,
    ['projection_incomplete'] AS gaps,
    toUInt8(0) AS source_priority,
    toUInt64(0) AS projection_version
FROM talgent_trace.otel_logs
WHERE agent_work_id != '';

-- ClickHouse expands SQL UDFs when creating views. Refresh the trigger with
-- every rule deployment. Measurements already derived keep the rules they were
-- derived with until they expire.
DROP VIEW IF EXISTS talgent_trace.trace_spans_measurements_mv;
CREATE MATERIALIZED VIEW talgent_trace.trace_spans_measurements_mv
TO talgent_trace.trace_work_measurements AS
SELECT
    concat('span:', TraceId, ':', SpanId) AS evidence_id,
    if(SpanName = 'claude_code.tool' AND SpanAttributes['tool_use_id'] != '',
       concat('call:', TraceId, ':', SpanAttributes['tool_use_id']), evidence_id) AS measurement_id,
    Timestamp AS timestamp,
    ResourceAttributes['talgent.project_id'] AS project_id,
    ResourceAttributes['talgent.agent_work_id'] AS agent_work_id,
    ResourceAttributes['talgent.agent_id'] AS agent_id,
    ResourceAttributes['talgent.runtime_purpose'] AS runtime_purpose,
    ResourceAttributes['talgent.owner_context'] AS owner_context,
    ResourceAttributes['talgent.owner_id'] AS owner_id,
    ResourceAttributes['talgent.user_id'] AS user_id,
    if(SpanName = 'claude_code.llm_request', if(SpanAttributes['model'] != '', SpanAttributes['model'], 'Unknown model'),
       talgent_call_dimension(SpanAttributes['tool_name'], SpanAttributes)) AS dimension,
    talgent_span_measurements(SpanName, SpanAttributes) AS measurements,
    arrayFilter(x -> x != '', [
       if(SpanName = 'claude_code.tool' AND SpanAttributes['tool_name'] = '', 'unclassified_call', ''),
       if(SpanName = 'claude_code.tool' AND SpanAttributes['tool_use_id'] = '', 'missing_call_id', ''),
       if(SpanName = 'claude_code.llm_request' AND SpanAttributes['model'] = '', 'missing_model', ''),
       if(talgent_span_category(SpanName, SpanAttributes) = 'other', 'unmapped_span', '')]) AS gaps,
    toUInt8(2) AS source_priority,
    toUInt64(toUnixTimestamp64Nano(now64(9))) AS projection_version
FROM talgent_trace.otel_traces
WHERE agent_work_id != ''
;

-- ClickHouse expands SQL UDFs when creating views. Refresh the trigger with
-- every rule deployment. Measurements already derived keep the rules they were
-- derived with until they expire.
DROP VIEW IF EXISTS talgent_trace.trace_logs_measurements_mv;
CREATE MATERIALIZED VIEW talgent_trace.trace_logs_measurements_mv
TO talgent_trace.trace_work_measurements AS
WITH
    LogAttributes['event.name'] AS event,
    mapUpdate(LogAttributes, map(
        'skill_name', JSONExtractString(LogAttributes['tool_parameters'], 'skill_name'),
        'subagent_type', JSONExtractString(LogAttributes['tool_parameters'], 'subagent_type'),
        'mcp_server_name', JSONExtractString(LogAttributes['tool_parameters'], 'mcp_server_name'),
        'mcp_tool_name', JSONExtractString(LogAttributes['tool_parameters'], 'mcp_tool_name'))) AS call_attributes
SELECT
    talgent_log_evidence_id(TraceId, Timestamp, Body, LogAttributes) AS evidence_id,
    if(event IN ('tool_decision', 'tool_result') AND LogAttributes['tool_use_id'] != '',
       concat('call:', TraceId, ':', LogAttributes['tool_use_id']), evidence_id) AS measurement_id,
    Timestamp AS timestamp,
    ResourceAttributes['talgent.project_id'] AS project_id,
    ResourceAttributes['talgent.agent_work_id'] AS agent_work_id,
    ResourceAttributes['talgent.agent_id'] AS agent_id,
    ResourceAttributes['talgent.runtime_purpose'] AS runtime_purpose,
    ResourceAttributes['talgent.owner_context'] AS owner_context,
    ResourceAttributes['talgent.owner_id'] AS owner_id,
    ResourceAttributes['talgent.user_id'] AS user_id,
    if(event = 'skill_activated', if(LogAttributes['skill.name'] != '', LogAttributes['skill.name'], 'Unknown skill'),
       talgent_call_dimension(LogAttributes['tool_name'], call_attributes)) AS dimension,
    multiIf(event = 'skill_activated', [('skill_activation', CAST(1 AS Nullable(Float64)))],
       event IN ('tool_decision', 'tool_result') AND LogAttributes['tool_use_id'] != '',
         [(talgent_call_kind(LogAttributes['tool_name'], call_attributes['mcp_server_name']), CAST(1 AS Nullable(Float64)))],
       CAST([], 'Array(Tuple(String, Nullable(Float64)))')) AS measurements,
    arrayFilter(x -> x != '', [
       if(event IN ('tool_decision', 'tool_result') AND talgent_call_kind(LogAttributes['tool_name'], call_attributes['mcp_server_name']) = 'unclassified_call', 'unclassified_call', ''),
       if(event IN ('tool_decision', 'tool_result') AND LogAttributes['tool_use_id'] = '', 'missing_call_id', ''),
       if(event = 'skill_activated' AND LogAttributes['event.sequence'] = '', 'missing_event_identity', '')]) AS gaps,
    toUInt8(1) AS source_priority,
    toUInt64(toUnixTimestamp64Nano(now64(9))) AS projection_version
FROM talgent_trace.otel_logs
WHERE agent_work_id != ''
;

-- Collapse native retry copies, then choose the strongest representation of
-- each call. Both count and usage queries consume these same contributions.
-- Preserve measurement time so arbitrary query windows filter facts before
-- grouping. Filtering rounded minute starts can discard an in-window request.
CREATE OR REPLACE VIEW talgent_trace.trace_metric_buckets AS
SELECT
    fact.1 AS measurement_time,
    toDateTime64(toStartOfInterval(fact.1, INTERVAL 60 SECOND), 9, 'UTC') AS bucket_start,
    toUInt32(60) AS bucket_seconds,
    project_id, agent_work_id,
    fact.2 AS agent_id, fact.3 AS runtime_purpose, fact.4 AS owner_context,
    fact.5 AS owner_id, fact.6 AS user_id,
    metric.1 AS metric_kind, fact.7 AS dimension,
    ifNull(metric.2, 0) AS value, toUInt64(isNull(metric.2)) AS unknown_count,
    fact.9 AS gaps
FROM (
    SELECT project_id, agent_work_id, measurement_id,
        argMax(tuple(timestamp, agent_id, runtime_purpose, owner_context, owner_id,
                     user_id, dimension, measurements, gaps),
               tuple(source_priority, projection_version, timestamp, evidence_id)) AS fact
    FROM talgent_trace.trace_work_measurements FINAL
    GROUP BY project_id, agent_work_id, measurement_id
)
ARRAY JOIN if(empty(fact.8) AND notEmpty(fact.9),
    [('observation_gap', CAST(NULL AS Nullable(Float64)))], fact.8) AS metric;
