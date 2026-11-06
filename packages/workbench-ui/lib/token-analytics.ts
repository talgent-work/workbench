import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@tanstack/react-query";
import { dashboardReportPeriod, previousReportPeriod, type DashboardReportPeriod } from "./dashboard-report";
import { type DashboardWindow } from "./dashboard";
import {
  buildTraceMetricLeaderboards,
  buildTraceMetricsDiagnostics,
  type TraceMetricBucketLike,
  type TraceDiagnostics,
  type TraceMetricLeaderboards,
  type TraceMetricsLike,
  type TraceTokenTrendPoint,
} from "./trace-view-model";
import { addTokenMeasurement, addTokenUsage, emptyTokenUsage, tokenMetricKinds, tokenTotal, tokenUsageFromMeasurements, type TokenUsage } from "./token-usage";

export type TokenAnalytics = {
  window: DashboardWindow;
  period: DashboardReportPeriod;
  previous: TokenAnalytics["tokenValues"] | null;
  comparisonUnavailable: string;
  tokenValues: TokenUsage & {total: number};
  metrics: TraceMetricsLike;
  summary: TraceDiagnostics["summary"];
  tokenTrend: TraceTokenTrendPoint[];
  modelUsage: ModelUsageTrend;
  leaderboards: TraceMetricLeaderboards;
};

/** Each window's usage per model: the window's five largest models by Total
 * Tokens individually, the rest combined as Other. */
export type ModelUsageTrend = {
  models: string[];
  buckets: Array<{label: string; usage: Record<string, TokenUsage>}>;
};

export const otherModels = "Other";
const shownModelCount = 5;

const refreshIntervalMs = 10000;

/** Spend's analytics over whatever `read` measures, such as a Project's or a
 * person's usage, with the previous period for comparison. `scope` names
 * what is measured. */
export function useTokenAnalytics(scope: readonly [string, string], read: (bounds: DashboardReportPeriod) => Promise<TraceMetricsLike>, window: DashboardWindow = "24h", period?: DashboardReportPeriod, enabled = true) {
  return useQuery({
    queryKey: [...scope, window, period?.end.getTime()],
    queryFn: async (): Promise<TokenAnalytics> => {
      const metricWindow = period ?? dashboardReportPeriod(window, new Date());
      const [current, previous] = await Promise.allSettled([
        read(metricWindow),
        window === "30d" ? Promise.resolve(null) : read(previousReportPeriod(metricWindow)),
      ]);
      for (const result of [current, previous]) {
        if (result.status === "rejected" && [Code.PermissionDenied, Code.Unauthenticated].includes(ConnectError.from(result.reason).code)) throw result.reason;
      }
      if (current.status === "rejected") throw current.reason;
      return {
        ...buildTokenAnalytics(current.value, window, metricWindow),
        previous: previous.status === "fulfilled" && previous.value ? buildTokenAnalytics(previous.value, window, previousReportPeriod(metricWindow)).tokenValues : null,
        comparisonUnavailable: window === "30d" ? "The previous 30D period is outside the 30-day metrics retention window." : previous.status === "rejected" ? `Previous metrics unavailable: ${previous.reason instanceof Error ? previous.reason.message : "query failed"}` : "",
      };
    },
    enabled,
    // A new period keeps showing the last report of the same scope and window until it answers.
    placeholderData: (previous, previousQuery) => previousQuery?.queryKey[0] === scope[0] && previousQuery?.queryKey[1] === scope[1] && previousQuery?.queryKey[2] === window ? previous : undefined,
    retry: false,
    refetchInterval: refreshIntervalMs,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
}

export function buildTokenAnalytics(traceMetrics: TraceMetricsLike, window: DashboardWindow, metricWindow = dashboardReportPeriod(window, new Date())): TokenAnalytics {
  const diagnostics = buildTraceMetricsDiagnostics(traceMetrics);
  const usage = tokenUsageFromMeasurements(traceMetrics.totals);
  const trends = buildTokenTrends(traceMetrics.buckets, metricWindow);
  return {
    window,
    period: metricWindow,
    previous: null,
    comparisonUnavailable: "Comparison not loaded",
    tokenValues: {...usage, total: tokenTotal(usage)},
    metrics: traceMetrics,
    summary: diagnostics.summary,
    tokenTrend: trends.tokenTrend,
    modelUsage: trends.modelUsage,
    leaderboards: buildTraceMetricLeaderboards(traceMetrics, 5),
  };
}

function buildTokenTrends(metrics: TraceMetricBucketLike[], metricWindow: DashboardReportPeriod): Pick<TokenAnalytics, "tokenTrend" | "modelUsage"> {
  const stepMillis = metricWindow.stepSeconds * 1000;
  const startMillis = metricWindow.start.getTime();
  const endMillis = metricWindow.end.getTime();
  const buckets = Array.from({ length: metricWindow.bucketCount }, (_, index) => ({
    label: formatTrendWindow(startMillis + index * stepMillis, startMillis + (index + 1) * stepMillis),
    usage: emptyTokenUsage(),
    models: new Map<string, TokenUsage>(),
  }));

  for (const metric of metrics) {
    if (!metric.bucketStart) continue;
    const bucketMillis = timestampDate(metric.bucketStart).getTime();
    if (bucketMillis >= endMillis || bucketMillis + metric.bucketSeconds * 1000 <= startMillis) continue;
    const bucket = buckets[Math.min(Math.max(Math.floor((bucketMillis - startMillis) / stepMillis), 0), buckets.length - 1)];
    const value = Number.isFinite(metric.value) ? metric.value : 0;
    addTokenMeasurement(bucket.usage, metric.metricKind, value);
    // Token measurements of a model call carry the model as their dimension.
    const model = metric.dimension.trim();
    if (!model || !tokenMetricKinds.includes(metric.metricKind)) continue;
    const usage = bucket.models.get(model) ?? emptyTokenUsage();
    addTokenMeasurement(usage, metric.metricKind, value);
    bucket.models.set(model, usage);
  }

  const windowTotals = new Map<string, number>();
  for (const bucket of buckets) for (const [model, usage] of bucket.models) windowTotals.set(model, (windowTotals.get(model) ?? 0) + tokenTotal(usage));
  const ranked = [...windowTotals].filter(([, total]) => total > 0).sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0])).map(([model]) => model);
  const shown = ranked.slice(0, shownModelCount);
  const models = ranked.length > shown.length ? [...shown, otherModels] : shown;

  return {
    tokenTrend: buckets.map(bucket => ({ label: bucket.label, ...bucket.usage, totalTokens: tokenTotal(bucket.usage) })),
    modelUsage: {
      models,
      buckets: buckets.map(bucket => {
        const usage: Record<string, TokenUsage> = {};
        for (const [model, value] of bucket.models) {
          const key = shown.includes(model) ? model : otherModels;
          usage[key] = addTokenUsage(usage[key] ?? emptyTokenUsage(), value);
        }
        return { label: bucket.label, usage };
      }),
    },
  };
}

function formatTrendWindow(startEpochMillis: number, endEpochMillis: number): string {
  const start = new Date(startEpochMillis);
  const end = new Date(endEpochMillis);
  if (sameUtcDay(start, end)) {
    return `${formatUtcClock(start)}-${formatUtcClock(end)}`;
  }
  return `${formatUtcDateTime(start)}-${formatUtcDateTime(end)}`;
}

function sameUtcDay(left: Date, right: Date): boolean {
  return left.getUTCFullYear() === right.getUTCFullYear()
    && left.getUTCMonth() === right.getUTCMonth()
    && left.getUTCDate() === right.getUTCDate();
}

function formatUtcClock(value: Date): string {
  return [
    value.getUTCHours(),
    value.getUTCMinutes(),
    value.getUTCSeconds(),
  ].map(padTimePart).join(":");
}

function formatUtcDateTime(value: Date): string {
  return `${padTimePart(value.getUTCMonth() + 1)}-${padTimePart(value.getUTCDate())} ${formatUtcClock(value)}`;
}

function padTimePart(value: number): string {
  return String(value).padStart(2, "0");
}
