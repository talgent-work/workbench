import type { ReactNode } from "react";
import { Card, Chip, Tooltip, Typography } from "@heroui/react";
import { ArrowDown, ArrowUpRight, Box, CheckCheck, DatabaseZap, Medal } from "lucide-react";
import { DistributionChart, MetricCard, TimeChart } from "./dashboard-charts";
import { ModelUsageChart } from "./model-usage-chart";
import { formatDashboardNumber } from "../../lib/dashboard";
import { reportBucketLabels } from "../../lib/dashboard-report";
import { otherModels, type TokenAnalytics } from "../../lib/token-analytics";

const colors = { input: "#0a8f76", cacheRead: "#2563eb", cacheWrite: "#7c3aed", output: "#d97706" };
const tokenSeries = [{ key: "input", label: "Input", color: colors.input }, { key: "cacheRead", label: "Cache read", color: colors.cacheRead }, { key: "cacheWrite", label: "Cache write", color: colors.cacheWrite }, { key: "output", label: "Output", color: colors.output }] as const;
// Models and call mixes share one categorical order; Other is always neutral.
const palette = ["#26d0a8", "#2563eb", "#7c3aed", "#db2777", "#d97706"];
const shown = (value: number) => formatDashboardNumber(value);

/** One ranking of the report: who or what used the most tokens. */
export type TokenRanking = { title: string; rows: Array<{ key: string; label: string; totalTokens: number; icon: ReactNode; detail?: string }> };

/** Spend's report of token usage over a window: metric cards with the
 * previous period, the token and model trends, the rankings and the call mixes. */
export function TokenReport({ analytics, rankings, hover, onHover }: {
  analytics: TokenAnalytics; rankings: TokenRanking[];
  hover: { index: number; chart: string } | null; onHover: (hover: { index: number; chart: string } | null) => void;
}) {
  const { summary, period, tokenValues, previous } = analytics;
  const points = analytics.tokenTrend.map((token, index) => ({ ...reportBucketLabels(period, index), input: token.input, cacheRead: token.cacheRead, cacheWrite: token.cacheWrite, output: token.output }));
  const modelPoints = analytics.modelUsage.buckets.map((bucket, index) => ({ ...reportBucketLabels(period, index), usage: bucket.usage }));
  const modelColors = Object.fromEntries(analytics.modelUsage.models.map((model, index) => [model, model === otherModels ? "var(--muted)" : palette[index % palette.length]]));
  const metrics = [
    { key: "total", label: "Total", hint: "Input + Cache read + Cache write + Output.", Icon: Box, color: "#64748b" },
    { key: "input", label: "Input", hint: "Input tokens excluding cache reads and writes.", Icon: ArrowDown, color: colors.input },
    { key: "cacheRead", label: "Cache read", hint: "Input tokens read from the prompt cache.", Icon: CheckCheck, color: colors.cacheRead },
    { key: "cacheWrite", label: "Cache write", hint: "Input tokens written to the prompt cache.", Icon: DatabaseZap, color: colors.cacheWrite },
    { key: "output", label: "Output", hint: "Model-generated output tokens.", Icon: ArrowUpRight, color: colors.output },
  ] as const;
  if (!analytics.metrics.totals.length && !analytics.metrics.buckets.length) {
    return <Typography type="body-sm" color="muted" role="status">No Token records received in this window.</Typography>;
  }
  return <div className="p-analytics" aria-label="Token analytics">
    {analytics.comparisonUnavailable && <Typography type="body-sm" color="muted" role="status">{analytics.comparisonUnavailable}</Typography>}
    <div className="p-metric-grid p-token-metrics">{metrics.map(metric => <MetricCard key={metric.key} label={metric.label} hint={metric.hint} icon={<metric.Icon size={17} color={metric.color} />} value={shown(tokenValues[metric.key])} comparison={{ current: tokenValues[metric.key], previous: previous?.[metric.key] ?? null, period: analytics.window.toUpperCase(), bounds: period }} />)}</div>
    <div className="p-trace-charts">
      <TimeChart title="Token usage" kind="area" unit="tokens" points={points} series={[...tokenSeries]} summary={`${shown(tokenValues.total)} tokens`} formatValue={shown} hover={hover} onHover={onHover} />
      <ModelUsageChart points={modelPoints} models={analytics.modelUsage.models} colors={modelColors} />
    </div>
    <div className={rankings.length > 1 ? "p-ranking-grid" : undefined}>{rankings.map(group => <Card key={group.title} role="group" aria-label={group.title}><Card.Header><Card.Title role="heading" aria-level={2}>{group.title}</Card.Title></Card.Header><Card.Content>
      {group.rows.length ? <ol className="p-ranking">{group.rows.map((row, index) => <li key={row.key}>
        <Chip size="sm" color={index === 0 ? "warning" : index === 1 ? "accent" : index === 2 ? "success" : "default"} variant="soft">{index < 3 && <Medal size={12} />}<Chip.Label>{index + 1}</Chip.Label></Chip>
        {row.icon}
        <Typography type="body-sm" truncate title={row.label}>{row.label}</Typography><Tooltip><Tooltip.Trigger className="shrink-0 ms-auto"><Typography type="body-sm" weight="medium">{shown(row.totalTokens)}</Typography></Tooltip.Trigger><Tooltip.Content>{row.totalTokens.toLocaleString()} tokens · {row.detail ?? row.key}</Tooltip.Content></Tooltip>
      </li>)}</ol> : <Typography type="body-sm" color="muted">No observations</Typography>}
    </Card.Content></Card>)}</div>
    <div className="p-trace-distributions">{[
      { title: "Model mix", data: summary.modelDistribution }, { title: "Tool mix", data: summary.toolDistribution }, { title: "Agent mix", data: summary.agentDistribution }, { title: "MCP mix", data: summary.mcpDistribution }, { title: "Skill mix", data: summary.skillDistribution },
    ].map(group => <DistributionChart key={group.title} title={group.title} unit="calls" data={group.data.map((row, index) => ({ ...row, color: palette[index % palette.length] }))} />)}</div>
  </div>;
}
