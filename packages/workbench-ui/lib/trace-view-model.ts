import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { tokenMetricKinds, tokenTotal, tokenUsageFromMeasurements, type TokenUsage } from "./token-usage";

export const traceMetricKinds = ["tool_call", "mcp_call", "skill_call", "agent_call", "skill_activation", "model_call", "input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens"] as const;

export interface TraceDiagnostics {
  summary: TraceSummary;
}

export interface TraceSummary {
  tokenUsage: TokenUsage;
  totalTokens: number;
  observationGaps: string[];
  incompleteMetricKinds: string[];
  modelCallCount: number;
  toolCallCount: number;
  skillCallCount: number;
  agentCallCount: number;
  mcpCallCount: number;
  skillActivationCount: number;
  toolDistribution: TraceMetricBar[];
  skillDistribution: TraceMetricBar[];
  agentDistribution: TraceMetricBar[];
  mcpDistribution: TraceMetricBar[];
  modelDistribution: TraceMetricBar[];
}

export interface TraceMetricBar {
  label: string;
  value: number;
  displayValue: string;
  percent: number;
}

export interface TraceTokenTrendPoint extends TokenUsage {
  label: string;
  totalTokens: number;
}

export interface TraceMetricBucketLike {
  bucketStart?: Timestamp;
  bucketSeconds: number;
  ownerContext?: string;
  ownerId?: string;
  runtimePurpose?: string;
  userId?: string;
  agentWorkId?: string;
  workCaller?: { kind: string; id: string };
  workKey?: { kind: string; id: string };
  metricKind: string;
  dimension: string;
  value: number;
  unknownCount?: bigint;
  gaps?: string[];
}

export interface TraceMetricTotalLike {
  metricKind: string;
  dimension: string;
  value: number;
  unknownCount?: bigint;
  gaps?: string[];
}

export interface TraceMetricsLike {
  buckets: TraceMetricBucketLike[];
  totals: TraceMetricTotalLike[];
}

export interface TraceTokenLeaderboardRow {
  key: string;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  percent: number;
}

export interface TraceOwnerRuntimeLeaderboardRow extends TraceTokenLeaderboardRow {
  ownerContext: string;
  runtimePurpose: string;
}

export interface TraceMetricLeaderboards {
  ownerRuntimes: TraceOwnerRuntimeLeaderboardRow[];
  users: TraceTokenLeaderboardRow[];
}


export function buildTraceMetricsDiagnostics(metrics: TraceMetricsLike): TraceDiagnostics {
  const tokenUsage = tokenUsageFromMeasurements(metrics.totals);
  const modelCallCount = metricKindTotal(metrics.totals, "model_call");
  const toolCallCount = metricKindTotal(metrics.totals, "tool_call");
  const skillCallCount = metricKindTotal(metrics.totals, "skill_call");
  const agentCallCount = metricKindTotal(metrics.totals, "agent_call");
  const mcpCallCount = metricKindTotal(metrics.totals, "mcp_call");
  const skillActivationCount = metricKindTotal(metrics.totals, "skill_activation");
  const observationGaps = [...new Set(metrics.buckets.flatMap((bucket) => bucket.gaps ?? []))];
  // Token usage is always the recorded amount, so only call kinds can be incomplete.
  const incompleteMetricKinds = new Set(metrics.totals.filter((metric) => (metric.unknownCount ?? 0n) > 0n && !tokenKinds.includes(metric.metricKind)).map((metric) => metric.metricKind));
  if (observationGaps.includes("projection_incomplete")) traceMetricKinds.filter((kind) => !tokenKinds.includes(kind)).forEach((kind) => incompleteMetricKinds.add(kind));
  if (observationGaps.some((gap) => gap === "missing_call_id" || gap === "unclassified_call")) {
    ["tool_call", "mcp_call", "skill_call", "agent_call"].forEach((kind) => incompleteMetricKinds.add(kind));
  }
  if (observationGaps.includes("missing_event_identity")) incompleteMetricKinds.add("skill_activation");

  return {
    summary: {
      tokenUsage,
      totalTokens: tokenTotal(tokenUsage),
      observationGaps,
      incompleteMetricKinds: [...incompleteMetricKinds],
      modelCallCount,
      toolCallCount,
      skillCallCount, agentCallCount, mcpCallCount, skillActivationCount,
      toolDistribution: metricDistribution(metrics.totals, "tool_call"),
      skillDistribution: metricDistribution(metrics.totals, "skill_call"),
      agentDistribution: metricDistribution(metrics.totals, "agent_call"),
      mcpDistribution: metricDistribution(metrics.totals, "mcp_call"),
      modelDistribution: metricDistribution(metrics.totals, "model_call"),
    },
  };
}

export function buildTraceMetricLeaderboards(metrics: TraceMetricsLike, limit = 5): TraceMetricLeaderboards {
  return {
    ownerRuntimes: ownerRuntimeLeaderboard(metrics.buckets, limit),
    users: tokenLeaderboard(metrics.buckets, "user", limit),
  };
}

const tokenKinds = tokenMetricKinds;

/**
 * What token usage is grouped by: a person, a Work, or the owner its Works
 * name through the caller attributes ownerContext and ownerId, as the caller
 * that started them names it.
 */
export type TokenAttribution = "user" | "work" | { ownerContext: string };

function attributionKey(metric: TraceMetricBucketLike, by: TokenAttribution): string {
  if (by === "user") return metric.userId?.trim() ?? "";
  if (by === "work") return metric.agentWorkId?.trim() ?? "";
  return metric.ownerContext === by.ownerContext ? metric.ownerId?.trim() ?? "" : "";
}

/** Total Tokens per person, Work or owner, for every one with usage (no ranking cut-off). */
export function tokenTotals(metrics: TraceMetricBucketLike[], by: TokenAttribution): Map<string, number> {
  return new Map(tokenLeaderboard(metrics, by, Number.POSITIVE_INFINITY).map(row => [row.key, row.totalTokens]));
}

export function formatTraceDuration(durationNanos: bigint): string {
  const millis = durationMillis(durationNanos);

  if (millis < 1000) {
    return `${formatCompactNumber(millis, Number.isInteger(millis) ? 0 : 1)}ms`;
  }

  const seconds = millis / 1000;
  if (seconds < 60) {
    return `${formatCompactNumber(seconds, 1)}s`;
  }

  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = Math.round(seconds % 60);
  return `${minutes}m ${remainingSeconds}s`;
}

function durationMillis(durationNanos: bigint): number {
  return Number(durationNanos) / 1_000_000;
}

function formatCompactNumber(value: number, fractionDigits: number): string {
  return value.toFixed(fractionDigits).replace(/\.0$/, "");
}

export function formatCompactTokenCount(value: number): string {
  if (value < 1000) {
    return `${Math.round(value)}`;
  }
  if (value < 1_000_000) {
    return `${formatCompactNumber(value / 1000, 1)}k`;
  }
  if (value < 1_000_000_000) {
    return `${formatCompactNumber(value / 1_000_000, 1)}M`;
  }
  return `${formatCompactNumber(value / 1_000_000_000, 1)}B`;
}

function metricKindTotal(metrics: TraceMetricTotalLike[], kind: string): number {
  return metrics
    .filter((metric) => metric.metricKind === kind)
    .reduce((sum, metric) => sum + safeMetricValue(metric.value), 0);
}

function metricDistribution(metrics: TraceMetricTotalLike[], kind: string): TraceMetricBar[] {
  const rows = metrics
    .filter((metric) => metric.metricKind === kind && metric.dimension.trim() !== "" && safeMetricValue(metric.value) > 0)
    .map((metric) => ({ label: metric.dimension, value: safeMetricValue(metric.value) }));
  const total = rows.reduce((sum, row) => sum + row.value, 0);

  return rows
    .sort((left, right) => right.value - left.value || left.label.localeCompare(right.label))
    .map((row) => ({
      label: row.label,
      value: row.value,
      displayValue: formatMetricValue(row.value),
      percent: total > 0 ? (row.value / total) * 100 : 0,
    }));
}

function safeMetricValue(value: number): number {
  return Number.isFinite(value) ? value : 0;
}

function formatMetricValue(value: number): string {
  return Number.isInteger(value) ? String(value) : formatCompactNumber(value, 1);
}

/** The limit people, Works or owners with the most Tokens, largest first. */
export function tokenLeaderboard(metrics: TraceMetricBucketLike[], by: TokenAttribution, limit = 5): TraceTokenLeaderboardRow[] {
  const totals = new Map<string, { inputTokens: number; outputTokens: number; cacheTokens: number }>();
  for (const metric of metrics) {
    const key = attributionKey(metric, by);
    if (!key) continue;
    const value = safeMetricValue(metric.value);
    if (!tokenKinds.includes(metric.metricKind)) continue;

    const current = totals.get(key) ?? { inputTokens: 0, outputTokens: 0, cacheTokens: 0 };
    if (metric.metricKind === "input_tokens") current.inputTokens += value;
    if (metric.metricKind === "output_tokens") current.outputTokens += value;
    if (metric.metricKind === "cache_read_tokens" || metric.metricKind === "cache_creation_tokens") current.cacheTokens += value;
    totals.set(key, current);
  }

  const rows = Array.from(totals.entries())
    .map(([key, value]) => ({
      key,
      inputTokens: value.inputTokens,
      outputTokens: value.outputTokens,
      totalTokens: value.inputTokens + value.outputTokens + value.cacheTokens,
      percent: 0,
    }))
    .filter((row) => row.totalTokens > 0)
    .sort((left, right) => right.totalTokens - left.totalTokens || left.key.localeCompare(right.key))
    .slice(0, Math.max(limit, 0));
  const maxTotal = Math.max(0, ...rows.map((row) => row.totalTokens));
  return rows.map((row) => ({
    ...row,
    percent: maxTotal > 0 ? Math.max((row.totalTokens / maxTotal) * 100, 4) : 0,
  }));
}

function ownerRuntimeLeaderboard(metrics: TraceMetricBucketLike[], limit: number): TraceOwnerRuntimeLeaderboardRow[] {
  const totals = new Map<string, { ownerContext: string; runtimePurpose: string; inputTokens: number; outputTokens: number; cacheTokens: number }>();
  for (const metric of metrics) {
    const ownerContext = metric.ownerContext?.trim() ?? "";
    const runtimePurpose = metric.runtimePurpose?.trim() ?? "";
    if (!ownerContext && !runtimePurpose) continue;
    if (!tokenKinds.includes(metric.metricKind)) continue;

    const key = `${ownerContext || "unknown"}:${runtimePurpose || "unknown"}`;
    const current = totals.get(key) ?? {
      ownerContext,
      runtimePurpose,
      inputTokens: 0,
      outputTokens: 0, cacheTokens: 0,
    };
    const value = safeMetricValue(metric.value);
    if (metric.metricKind === "input_tokens") current.inputTokens += value;
    if (metric.metricKind === "output_tokens") current.outputTokens += value;
    if (metric.metricKind === "cache_read_tokens" || metric.metricKind === "cache_creation_tokens") current.cacheTokens += value;
    totals.set(key, current);
  }

  const rows = Array.from(totals.entries())
    .map(([key, value]) => ({
      key,
      ownerContext: value.ownerContext,
      runtimePurpose: value.runtimePurpose,
      inputTokens: value.inputTokens,
      outputTokens: value.outputTokens,
      totalTokens: value.inputTokens + value.outputTokens + value.cacheTokens,
      percent: 0,
    }))
    .filter((row) => row.totalTokens > 0)
    .sort((left, right) => right.totalTokens - left.totalTokens || left.key.localeCompare(right.key))
    .slice(0, Math.max(limit, 0));
  const maxTotal = Math.max(0, ...rows.map((row) => row.totalTokens));
  return rows.map((row) => ({
    ...row,
    percent: maxTotal > 0 ? Math.max((row.totalTokens / maxTotal) * 100, 4) : 0,
  }));
}
