export type DashboardWindow = "24h" | "7d" | "30d";
export const dashboardWindows: DashboardWindow[] = ["24h", "7d", "30d"];

export function parseDashboardWindow(value: string | null | undefined): DashboardWindow {
  return value === "7d" || value === "30d" ? value : "24h";
}

export function formatDashboardNumber(value: number): string {
  if (value < 1000) return `${Math.round(value)}`;
  if (value < 1_000_000) return `${formatCompact(value / 1000, 1)}k`;
  if (value < 1_000_000_000) return `${formatCompact(value / 1_000_000, 1)}M`;
  return `${formatCompact(value / 1_000_000_000, 1)}B`;
}

export function formatDashboardTokens(value: number): string {
  return `${formatDashboardNumber(value)} tok`;
}

function formatCompact(value: number, fractionDigits: number): string {
  return value.toFixed(fractionDigits).replace(/\.0$/, "");
}
