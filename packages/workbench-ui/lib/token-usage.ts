/** Token usage in its four categories; Total Tokens is their sum. */
export interface TokenUsage {
  input: number;
  cacheRead: number;
  cacheWrite: number;
  output: number;
}

export const tokenCategories = [
  { key: "input", label: "Input", metricKind: "input_tokens" },
  { key: "cacheRead", label: "Cache read", metricKind: "cache_read_tokens" },
  { key: "cacheWrite", label: "Cache write", metricKind: "cache_creation_tokens" },
  { key: "output", label: "Output", metricKind: "output_tokens" },
] as const satisfies readonly { key: keyof TokenUsage; label: string; metricKind: string }[];

export const tokenMetricKinds: readonly string[] = tokenCategories.map(category => category.metricKind);

export const emptyTokenUsage = (): TokenUsage => ({ input: 0, cacheRead: 0, cacheWrite: 0, output: 0 });

export function tokenTotal(usage: TokenUsage): number {
  return usage.input + usage.cacheRead + usage.cacheWrite + usage.output;
}

/** Adds one measurement to usage; other metric kinds are ignored. */
export function addTokenMeasurement(usage: TokenUsage, metricKind: string, value: number): void {
  const category = tokenCategories.find(item => item.metricKind === metricKind);
  if (category && Number.isFinite(value)) usage[category.key] += value;
}

export function addTokenUsage(target: TokenUsage, usage: TokenUsage): TokenUsage {
  for (const { key } of tokenCategories) target[key] += usage[key];
  return target;
}

export function tokenUsageFromMeasurements(measurements: readonly { metricKind: string; value: number }[]): TokenUsage {
  const usage = emptyTokenUsage();
  for (const item of measurements) addTokenMeasurement(usage, item.metricKind, item.value);
  return usage;
}

/** A model call's usage as its span reports it. A call without usage, such as
 * a rejected request, counts as zero. */
export function tokenUsageFromAttributes(attributes: Record<string, string>): TokenUsage {
  const usage = emptyTokenUsage();
  for (const { key, metricKind } of tokenCategories) {
    const value = Number(attributes[metricKind]);
    if (attributes[metricKind]?.trim() && Number.isFinite(value) && value >= 0) usage[key] = value;
  }
  return usage;
}

/**
 * Cache Hit Rate = Cache read ÷ (Input + Cache read + Cache write). Callers sum
 * usage before asking, so an aggregate rate follows volume; null when there is
 * no input usage at all.
 */
export function cacheHitRate(usage: TokenUsage): number | null {
  const denominator = usage.input + usage.cacheRead + usage.cacheWrite;
  return denominator > 0 ? usage.cacheRead / denominator : null;
}

export function formatHitRate(rate: number): string {
  return `${(rate * 100).toFixed(1)}%`;
}
