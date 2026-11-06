import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";
import { buildTraceMetricLeaderboards, buildTraceMetricsDiagnostics, formatTraceDuration, tokenLeaderboard } from "../lib/trace-view-model";
import { cacheHitRate, tokenUsageFromAttributes } from "../lib/token-usage";

describe("native trace view model", () => {
  it("uses one native usage and call contract for summaries, trends and rankings", () => {
    const values = { input_tokens: 10, output_tokens: 5, cache_read_tokens: 100, cache_creation_tokens: 20, model_call: 2, tool_call: 2, agent_call: 3, mcp_call: 4, skill_call: 5, skill_activation: 1 };
    const totals = Object.entries(values).map(([metricKind, value]) => ({ metricKind, value, dimension: "fixture", unknownCount: 0n }));
    const buckets = totals.map((total) => ({ ...total, bucketStart: timestampFromDate(new Date("2026-09-20T00:00:00Z")), bucketSeconds: 60, ownerId: "case-1", userId: "reader", ownerContext: "case", runtimePurpose: "work" }));
    const { summary } = buildTraceMetricsDiagnostics({ totals, buckets });
    expect(summary.totalTokens).toBe(135);
    expect(summary.tokenUsage).toEqual({ input: 10, cacheRead: 100, cacheWrite: 20, output: 5 });
    expect(summary.agentCallCount).toBe(3);
    expect(summary.mcpCallCount).toBe(4);
    expect(summary.skillCallCount).toBe(5);
    expect(summary.skillActivationCount).toBe(1);
    expect(summary.toolCallCount).toBe(2);
    const rankings = buildTraceMetricLeaderboards({ totals, buckets });
    expect(tokenLeaderboard(buckets, { ownerContext: "case" })[0].totalTokens).toBe(135);
    expect(rankings.users[0].totalTokens).toBe(135);
    expect(rankings.ownerRuntimes[0].totalTokens).toBe(135);
  });

  // The caller names the owner its Works carry, such as a support case.
  it("groups usage by the owner its Works name, whatever their Agent", () => {
    const bucket = { bucketSeconds: 7200, dimension: "model", metricKind: "input_tokens", ownerContext: "case" };
    const ranking = tokenLeaderboard([
      { ...bucket, ownerId: "case-a", runtimePurpose: "work", value: 20 },
      { ...bucket, ownerId: "case-a", runtimePurpose: "employee", metricKind: "output_tokens", value: 5 },
      { ...bucket, ownerId: "case-a", metricKind: "cache_read_tokens", value: 10 },
      { ...bucket, ownerId: "case-b", value: 8 },
      { ...bucket, ownerContext: "caller", ownerId: "project-1", value: 50 },
      { ...bucket, ownerContext: undefined, ownerId: undefined, value: 100 },
    ], { ownerContext: "case" });
    expect(ranking.map(({ key, totalTokens }) => ({ key, totalTokens }))).toEqual([
      { key: "case-a", totalTokens: 35 },
      { key: "case-b", totalTokens: 8 },
    ]);
  });

  // A call without usage, such as a rejected request, consumed nothing to show.
  it("counts missing native usage as zero instead of marking usage incomplete", () => {
    const metrics = { buckets: [], totals: [
      { metricKind: "model_call", dimension: "model", value: 1, unknownCount: 0n },
      { metricKind: "input_tokens", dimension: "model", value: 10, unknownCount: 0n },
      { metricKind: "output_tokens", dimension: "model", value: 0, unknownCount: 1n },
      { metricKind: "cache_read_tokens", dimension: "model", value: 0, unknownCount: 0n },
      { metricKind: "cache_creation_tokens", dimension: "model", value: 0, unknownCount: 0n },
    ] };
    const { summary } = buildTraceMetricsDiagnostics(metrics);
    expect(summary.totalTokens).toBe(10);
    expect(summary.tokenUsage).toEqual({ input: 10, cacheRead: 0, cacheWrite: 0, output: 0 });
  });

  it("discloses a failed projection as a gap while showing recorded usage", () => {
    const { summary } = buildTraceMetricsDiagnostics({
      buckets: [{ metricKind: "observation_gap", dimension: "", value: 0, bucketSeconds: 60, unknownCount: 1n, gaps: ["projection_incomplete"] }],
      totals: [{ metricKind: "input_tokens", dimension: "model", value: 10 }, { metricKind: "observation_gap", dimension: "", value: 0, unknownCount: 1n }],
    });
    expect(summary.totalTokens).toBe(10);
    expect(summary.incompleteMetricKinds).toContain("agent_call");
    expect(summary.observationGaps).toEqual(["projection_incomplete"]);
  });

  it("does not invalidate known usage for an unmapped native detail", () => {
    const { summary } = buildTraceMetricsDiagnostics({
      buckets: [{ metricKind: "observation_gap", dimension: "", value: 0, bucketSeconds: 60, gaps: ["unmapped_span"] }],
      totals: [{ metricKind: "input_tokens", dimension: "model", value: 10 }],
    });
    expect(summary.totalTokens).toBe(10);
    expect(summary.incompleteMetricKinds).toEqual([]);
    expect(summary.observationGaps).toEqual(["unmapped_span"]);
  });

  // Intent (real): duration labels must stay compact across sub-second and second boundaries.
  it("formats trace durations from nanoseconds", () => {
    expect(formatTraceDuration(500_000n)).toBe("0.5ms");
    expect(formatTraceDuration(42_000_000n)).toBe("42ms");
    expect(formatTraceDuration(2_500_000_000n)).toBe("2.5s");
  });


  it("reads a model call's four categories, counting missing or invalid values as zero", () => {
    const usage = { input_tokens: "10", output_tokens: "5", cache_read_tokens: "100", cache_creation_tokens: "20" };
    expect(tokenUsageFromAttributes(usage)).toEqual({ input: 10, cacheRead: 100, cacheWrite: 20, output: 5 });
    expect(tokenUsageFromAttributes({})).toEqual({ input: 0, cacheRead: 0, cacheWrite: 0, output: 0 });
    expect(tokenUsageFromAttributes({ ...usage, output_tokens: "-5", input_tokens: "invalid" })).toEqual({ input: 0, cacheRead: 100, cacheWrite: 20, output: 0 });
  });

  // Cache Hit Rate = Cache read ÷ (Input + Cache read + Cache write), hidden without input.
  it("rates cache hits over all input and has no rate without input", () => {
    expect(cacheHitRate({ input: 257, cacheRead: 6150, cacheWrite: 0, output: 74 })).toBeCloseTo(6150 / 6407);
    expect(cacheHitRate({ input: 10, cacheRead: 60, cacheWrite: 30, output: 5 })).toBe(.6);
    expect(cacheHitRate({ input: 0, cacheRead: 0, cacheWrite: 0, output: 9 })).toBeNull();
  });

  it("keeps unnamed calls in distributions and activation out of call totals", () => {
    const { summary } = buildTraceMetricsDiagnostics({ buckets: [], totals: [
      { metricKind: "skill_call", dimension: "Unknown skill", value: 2 },
      { metricKind: "skill_call", dimension: "review", value: 1 },
      { metricKind: "skill_activation", dimension: "review", value: 4 },
      { metricKind: "agent_call", dimension: "Explore", value: 5 },
      { metricKind: "mcp_call", dimension: "mcp__probe__read", value: 6 },
    ] });
    expect(summary.skillCallCount).toBe(3);
    expect(summary.skillActivationCount).toBe(4);
    expect(summary.skillDistribution.map((bar) => [bar.label, bar.value])).toEqual([["Unknown skill", 2], ["review", 1]]);
    expect(summary.agentDistribution[0]).toMatchObject({ label: "Explore", value: 5 });
    expect(summary.mcpDistribution[0]).toMatchObject({ label: "mcp__probe__read", value: 6 });
    expect(summary.toolCallCount).toBe(0);
  });
});
