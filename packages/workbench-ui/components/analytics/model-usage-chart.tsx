// Grouped bars: one per model in each window, height its Total Tokens. Hover,
// focus or tap a bar for that model's four categories and Cache hit rate.
import { useState } from "react";
import { typographyVariants, Button, Card, Chip, Disclosure, Table, Tooltip, Typography } from "@heroui/react";
import { niceAxisMax, pointY, xTickIndices, yTickValues } from "./chart-geometry";
import { formatDashboardNumber } from "../../lib/dashboard";
import { tokenTotal, type TokenUsage } from "../../lib/token-usage";
import { formatCompactTokenCount } from "../../lib/trace-view-model";
import { TokenBreakdown } from "./token-breakdown";

export type ModelUsagePoint = { label: string; interval: string; usage: Record<string, TokenUsage> };

export function ModelUsageChart({ points, models, colors }: { points: ModelUsagePoint[]; models: string[]; colors: Record<string, string> }) {
  const [selected, setSelected] = useState<{ bucket: number; model: number } | null>(null);
  const [plotWidth, setPlotWidth] = useState(0);
  const total = (bucket: number, model: number) => { const usage = points[bucket]?.usage[models[model]]; return usage ? tokenTotal(usage) : 0; };
  // Keyboard moves through the bars that exist, in reading order.
  const bars = points.flatMap((_, bucket) => models.flatMap((__, model) => total(bucket, model) > 0 ? [{ bucket, model }] : []));
  const maximum = niceAxisMax(Math.max(1, ...bars.map(bar => total(bar.bucket, bar.model))));
  const ticks = yTickValues(maximum);
  const groupWidth = 100 / Math.max(points.length, 1);
  const barWidth = groupWidth * .8 / Math.max(models.length, 1);
  const barX = (bucket: number, model: number) => bucket * groupWidth + groupWidth * .1 + model * barWidth;
  const grandTotal = bars.reduce((sum, bar) => sum + total(bar.bucket, bar.model), 0);
  const usage = selected ? points[selected.bucket].usage[models[selected.model]] : undefined;
  const select = (bar: { bucket: number; model: number } | undefined, element: Element) => {
    if (!bar) return;
    setPlotWidth(element.getBoundingClientRect().width);
    setSelected(bar);
  };
  const step = (offset: number, element: Element) => {
    const index = selected ? bars.findIndex(bar => bar.bucket === selected.bucket && bar.model === selected.model) : -1;
    select(bars[Math.max(0, Math.min(bars.length - 1, index + offset))], element);
  };
  const describe = (bar: { bucket: number; model: number }) => `${points[bar.bucket].interval}, ${models[bar.model]}: ${total(bar.bucket, bar.model).toLocaleString()} tokens`;

  return <Card role="group" className="p-plot-card" aria-label="Model usage">
    <Card.Header><div className="p-section-title"><Card.Title role="heading" aria-level={2}>Model usage</Card.Title><Chip size="sm">{formatDashboardNumber(grandTotal)} tokens</Chip></div></Card.Header>
    <Card.Content>
      {models.length > 0 && <div className="p-chart-legend" role="list" aria-label="Models">{models.map(model => <span role="listitem" className="p-series-label" key={model}><span className="p-series-dot"><svg width="8" height="8" aria-hidden="true"><circle cx="4" cy="4" r="3" fill={colors[model]} /></svg></span><span className={typographyVariants({ type: "body-xs" }).base()}>{model}</span></span>)}</div>}
      {bars.length ? <Tooltip delay={0} closeDelay={100} isOpen={Boolean(usage)} onOpenChange={open => { if (!open) setSelected(null); }}>
        <Tooltip.Trigger className="p-time-plot" role="slider" aria-label="Explore Model usage. Use arrow keys to move between bars."
          aria-valuemin={1} aria-valuemax={bars.length} aria-valuenow={selected ? bars.findIndex(bar => bar.bucket === selected.bucket && bar.model === selected.model) + 1 : 1}
          aria-valuetext={describe(selected ?? bars[0])}
          onFocus={event => { if (!selected) select(bars[0], event.currentTarget); }}
          onKeyDown={event => {
            if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
              event.preventDefault();
              if (event.key === "Home") select(bars[0], event.currentTarget);
              else if (event.key === "End") select(bars.at(-1), event.currentTarget);
              else step(event.key === "ArrowRight" ? 1 : -1, event.currentTarget);
            }
            if (event.key === "Escape") setSelected(null);
          }}>
          <div className="p-y-axis">{ticks.map(tick => <span className={typographyVariants({ type: "body-xs" }).base()} key={tick} style={{ top: `${pointY(tick, maximum)}%` }}>{formatDashboardNumber(tick)}</span>)}</div>
          <svg viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label={`Model usage: ${points.length} time buckets. Exact values in View data.`}>
            {ticks.map(tick => <path key={tick} d={`M 0 ${pointY(tick, maximum)} H 100`} stroke="var(--separator)" strokeWidth="1" strokeDasharray="3 4" vectorEffect="non-scaling-stroke" />)}
            {bars.map(bar => {
              const value = total(bar.bucket, bar.model);
              const active = selected?.bucket === bar.bucket && selected.model === bar.model;
              return <rect key={`${bar.bucket}-${bar.model}`} className="p-bar-mark" data-model={models[bar.model]} x={barX(bar.bucket, bar.model)} y={pointY(value, maximum)} width={barWidth * .85} height={88 - pointY(value, maximum)}
                fill={colors[models[bar.model]]} opacity={!selected || active ? 1 : .45}
                onPointerEnter={event => select(bar, event.currentTarget.closest(".p-time-plot") ?? event.currentTarget)}
                onPointerDown={event => select(bar, event.currentTarget.closest(".p-time-plot") ?? event.currentTarget)} />;
            })}
          </svg>
        </Tooltip.Trigger>
        <Tooltip.Content className="p-chart-hover-tip" placement="top" crossOffset={selected ? ((barX(selected.bucket, selected.model) + barWidth / 2) / 100 - .5) * plotWidth : 0}>
          {selected && usage && <>
            <Typography type="body-xs" color="muted" className="p-chart-hover-interval">{points[selected.bucket].interval}</Typography>
            <p className="p-chart-hover-head"><span className="p-series-label"><span className="p-series-dot"><svg width="8" height="8" aria-hidden="true"><circle cx="4" cy="4" r="3" fill={colors[models[selected.model]]} /></svg></span>{models[selected.model]}</span><strong>{formatCompactTokenCount(tokenTotal(usage))}</strong></p>
            <TokenBreakdown usage={usage} />
          </>}
        </Tooltip.Content>
      </Tooltip> : <Typography type="body-sm" className="p-chart-empty">No observations in this window.</Typography>}
      <div className="p-x-axis">{xTickIndices(points.length).map(index => <Typography type="body-xs" key={index} style={{ left: `${(index + .5) * groupWidth}%` }}>{points[index].label.split(" · ").map(part => <span key={part}>{part}</span>)}</Typography>)}</div>
      <Disclosure><Disclosure.Heading><Button slot="trigger" variant="ghost" size="sm" aria-label="View Model usage data"><span className={typographyVariants({ type: "body-xs", color: "muted" }).base()}>View data</span><Disclosure.Indicator /></Button></Disclosure.Heading><Disclosure.Content>
        <Table><Table.ScrollContainer><Table.Content aria-label="Model usage data"><Table.Header><Table.Column isRowHeader>Time</Table.Column>{models.map(model => <Table.Column key={model}>{model}</Table.Column>)}</Table.Header>
          <Table.Body>{points.map((point, bucket) => <Table.Row key={point.label}><Table.Cell>{point.interval}</Table.Cell>{models.map((model, index) => <Table.Cell key={model}>{total(bucket, index).toLocaleString()}</Table.Cell>)}</Table.Row>)}</Table.Body>
        </Table.Content></Table.ScrollContainer></Table>
      </Disclosure.Content></Disclosure>
    </Card.Content>
  </Card>;
}
