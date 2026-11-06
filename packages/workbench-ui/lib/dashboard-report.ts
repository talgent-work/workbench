import type { DashboardWindow } from "./dashboard";

export interface DashboardReportPeriod {
  start: Date;
  end: Date;
  stepSeconds: number;
  bucketCount: number;
}

// Fixed elapsed UTC intervals shared by both perspectives and their queries.
export function dashboardReportPeriod(range: DashboardWindow, now: Date): DashboardReportPeriod {
  const stepSeconds = range === "24h" ? 7200 : range === "7d" ? 43200 : 172800;
  const bucketCount = range === "24h" ? 12 : range === "7d" ? 14 : 15;
  const alignment = range === "24h" ? 7200000 : 86400000;
  const end = new Date((Math.floor(now.getTime() / alignment) + 1) * alignment);
  return { start: new Date(end.getTime() - stepSeconds * bucketCount * 1000), end, stepSeconds, bucketCount };
}

export function previousReportPeriod(period: DashboardReportPeriod): DashboardReportPeriod {
  return { ...period, start: new Date(period.start.getTime() - (period.end.getTime() - period.start.getTime())), end: period.start };
}

export function reportBucketLabels(period: DashboardReportPeriod, index: number) {
  const start = new Date(period.start.getTime() + index * period.stepSeconds * 1000);
  const end = new Date(start.getTime() + period.stepSeconds * 1000);
  const date = start.toLocaleDateString("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
  const time = start.toISOString().slice(11, 16);
  return {
    label: period.stepSeconds === 7200 ? time : period.stepSeconds === 43200 ? `${date} · ${time}` : date,
    interval: `${start.toISOString().slice(0,16).replace("T", " ")} – ${end.toISOString().slice(0,16).replace("T", " ")} UTC`,
  };
}

export function compareMetric(current: number | null, previous: number | null) {
  if (current === null || previous === null) return { direction: "unknown", percent: null } as const;
  if (previous === 0 && current !== 0) return { direction: "new", percent: null } as const;
  return { direction: current > previous ? "up" : current < previous ? "down" : "flat", percent: previous === 0 ? 0 : (current - previous) / previous * 100 } as const;
}
