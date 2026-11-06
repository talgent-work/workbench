// HeroUI owns card/control appearance. SVG marks reuse the existing chart geometry.
import { useState, type ReactNode } from "react";
import { typographyVariants, Avatar, Button, Card, Chip, Disclosure, Table, Tooltip, Typography } from "@heroui/react";
import { niceAxisMax, numericValue, pointY, stackedAreaPath, trendPath, xTickIndices, yTickValues } from "./chart-geometry";
import type { TrendSeries } from "./chart-geometry";
import { compareMetric, previousReportPeriod, type DashboardReportPeriod } from "../../lib/dashboard-report";
import { Activity, Archive, ArrowDownToLine, ArrowUpFromLine, Database, DatabaseZap, FilePlus2, MessageCircleQuestion, Sigma, ArrowDownRight, ArrowUpRight, Minus, type LucideIcon } from "lucide-react";
import { formatDashboardNumber } from "../../lib/dashboard";

export function MetricTrend({current, previous, period, snapshot = false, bounds}: {current: number | null; previous: number | null; period: string; snapshot?: boolean; bounds?: DashboardReportPeriod}) {
  const {direction, percent} = compareMetric(current, previous);
  const Icon = direction === "up" || direction === "new" ? ArrowUpRight : direction === "down" ? ArrowDownRight : Minus;
  const text = direction === "unknown" ? "—" : direction === "new" ? "New" : `${percent && Math.abs(percent) < .1 ? "<0.1" : Math.abs(percent ?? 0).toFixed(1)}%`;
  const description = direction === "unknown" ? "Comparison unavailable" : direction === "new" ? "Previous value was zero; percentage change is undefined" : `${direction === "up" ? "Up" : direction === "down" ? "Down" : "Unchanged"} ${text}`;
  const color = direction === "up" || direction === "new" ? "success" : direction === "down" ? "danger" : "default";
  const interval = (range: DashboardReportPeriod) => `${range.start.toISOString().slice(0, 16).replace("T", " ")} – ${range.end.toISOString().slice(0, 16).replace("T", " ")} UTC`;
  return <Tooltip><Tooltip.Trigger className="p-metric-trend" aria-label={`${description} versus previous ${period}`}><Chip size="sm" variant="soft" color={color}><Icon size={13} /><Chip.Label>{text}</Chip.Label></Chip></Tooltip.Trigger><Tooltip.Content><Typography type="body-sm">{description} · vs previous {period}</Typography><Typography type="body-xs">{current === null ? "—" : current.toLocaleString()} vs {previous === null ? "—" : previous.toLocaleString()}{snapshot ? " · period-end snapshots" : " · equal-length windows"}</Typography>{bounds && <><Typography type="body-xs">Current: {interval(bounds)}</Typography><Typography type="body-xs">Previous: {interval(previousReportPeriod(bounds))}</Typography></>}</Tooltip.Content></Tooltip>;
}

export function MetricCard({ label, value, hint, icon, comparison }: {label: string; value: string; hint?: string; icon: ReactNode; comparison: Parameters<typeof MetricTrend>[0]}) {
  const icons: Partial<Record<string, LucideIcon>> = { Created: FilePlus2, Active: Activity, Waiting: MessageCircleQuestion, Closed: Archive, Total: Sigma, Input: ArrowDownToLine, "Cache read": Database, "Cache write": DatabaseZap, Output: ArrowUpFromLine };
  const Icon = icons[label];
  return <Card role="group" aria-label={`${label} metric`}><Card.Content><div className="flex items-start gap-3">
    {(Icon || icon) && <Avatar size="sm" variant="soft" color="warning" aria-hidden="true"><Avatar.Fallback>{Icon ? <Icon size={18} strokeWidth={1.75} /> : icon}</Avatar.Fallback></Avatar>}
    <div className="min-w-0 flex-1"><Card.Title role="heading" aria-level={2}>{hint ? <Tooltip><Tooltip.Trigger aria-label={`${label} definition`}>{label}</Tooltip.Trigger><Tooltip.Content>{hint}</Tooltip.Content></Tooltip> : label}</Card.Title>
      <div className="p-metric-value mt-1"><p className={typographyVariants({ type: "h2" }).base()}>{value}</p><MetricTrend {...comparison} /></div>
    </div>
  </div></Card.Content></Card>;
}

// "1 call", not "1 calls": a plural unit reads singular for exactly one.
const unitOf = (unit: string, value: number) => value === 1 && unit.endsWith("s") ? unit.slice(0, -1) : unit;

export function TimeChart<PlotPoint extends {label: string; interval: string}>({ title, points, series, kind = "line", unit, hint, summary, missingValueLabel, formatValue, hover, onHover }: {title: string; points: PlotPoint[]; series: TrendSeries<PlotPoint>[]; kind?: "line" | "area" | "bar"; unit: string; hint?: string; summary?: string; missingValueLabel?: string; /** The hover card's value, in place of the full number and unit. */ formatValue?: (value: number) => string; hover: {index: number; chart: string} | null; onHover: (value: {index: number; chart: string} | null) => void}) {
  const [plotWidth, setPlotWidth] = useState(0);
  const [hidden, setHidden] = useState<string[]>([]);
  const active = series.filter(s => !hidden.includes(String(s.key)));
  const value = (p: PlotPoint, key: keyof PlotPoint) => Number(p[key]) || 0;
  const known = (p: PlotPoint, key: keyof PlotPoint) => typeof p[key] === "number" && Number.isFinite(p[key]);
  const display = (p: PlotPoint, key: keyof PlotPoint) => known(p, key) ? value(p, key).toLocaleString() : (missingValueLabel ?? "—");
  const incomplete = points.some(p => series.some(s => !known(p, s.key)));
  const maximum = niceAxisMax(Math.max(1, ...points.map(p => kind === "area" ? active.reduce((sum, s) => sum + value(p, s.key), 0) : Math.max(...series.map(s => value(p, s.key))))));
  const ticks = yTickValues(maximum);
  const x = (index: number) => (index + .5) * 100 / points.length;
  const total = points.reduce((sum, p) => sum + series.reduce((n, s) => n + value(p, s.key), 0), 0);
  const point = hover ? points[hover.index] : undefined;
  const isSource = hover?.chart === title;
  const selectBucket = (index: number, element: HTMLElement) => {
    setPlotWidth(element.getBoundingClientRect().width);
    if (hover?.index !== index || !isSource) onHover({index, chart: title});
  };
  const selectAt = (event: {clientX: number; currentTarget: HTMLElement}) => {
    const box = event.currentTarget.getBoundingClientRect();
    const fraction = Math.max(0, Math.min(1, (event.clientX - box.left) / box.width));
    selectBucket(Math.min(points.length - 1, Math.floor(fraction * points.length)), event.currentTarget);
  };
  const bandStart = hover ? hover.index * 100 / points.length : 0;
  const bandEnd = hover ? (hover.index + 1) * 100 / points.length : 0;
  return <Card role="group" className="p-plot-card" aria-label={title}>
    <Card.Header><div className="p-section-title"><Card.Title role="heading" aria-level={2}>{hint ? <Tooltip><Tooltip.Trigger aria-label={`${title} definition`}>{title}</Tooltip.Trigger><Tooltip.Content>{hint}</Tooltip.Content></Tooltip> : title}</Card.Title>{summary !== "" && <Chip size="sm">{summary ?? (incomplete ? "Incomplete data" : `${formatDashboardNumber(total)} ${unitOf(unit, total)}`)}</Chip>}</div></Card.Header>
    <Card.Content>
      {incomplete && !missingValueLabel && <Typography type="body-sm" color="muted">Some values are unavailable. Missing values appear as — in the data table.</Typography>}
      {series.length > 1 && <div className="p-chart-legend" role="group" aria-label={`${title} series`}>{series.map(s => {
        const selected = !hidden.includes(String(s.key));
        const lastVisible = active.length === 1 && active[0].key === s.key;
        return <Chip<"button"> key={String(s.key)} size="sm" variant={selected ? "secondary" : "tertiary"} className="p-series-chip" render={props => <button {...props} type="button" />}
          aria-pressed={selected} aria-disabled={lastVisible || undefined} onClick={() => {if (!lastVisible) setHidden(keys => selected ? [...keys, String(s.key)] : keys.filter(k => k !== s.key));}}>
          <span className="p-series-label"><span className="p-series-dot"><svg width="8" height="8" aria-hidden="true"><circle cx="4" cy="4" r="3" fill={selected ? s.color : "none"} stroke={s.color} /></svg></span><span className={typographyVariants({ type: "body-xs", color: selected ? "default" : "muted" }).base()}  >{s.label}</span></span>
        </Chip>;
      })}</div>}

      {points.length ? <Tooltip delay={0} closeDelay={100} isOpen={!!point && isSource} onOpenChange={open => {if (!open && isSource) onHover(null);}}><Tooltip.Trigger className="p-time-plot" role="slider" aria-valuemin={1} aria-valuemax={points.length} aria-valuenow={(hover?.index ?? 0) + 1} aria-valuetext={`${(point ?? points[0]).interval}. ${active.map(s => `${s.label}: ${display(point ?? points[0], s.key)} ${unitOf(unit, numericValue(point ?? points[0], s.key))}`).join(". ")}`} aria-label={`Explore ${title} timeline. Use arrow keys to select a time interval.`}
        onPointerMove={selectAt} onPointerDown={selectAt}
        onFocus={event => selectBucket(hover?.index ?? 0, event.currentTarget)}
        onKeyDown={event => {
          if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
            event.preventDefault();
            const index = event.key === "Home" ? 0 : event.key === "End" ? points.length - 1 : Math.max(0, Math.min(points.length - 1, (hover?.index ?? 0) + (event.key === "ArrowRight" ? 1 : -1)));
            selectBucket(index, event.currentTarget);
          }
          if (event.key === "Escape") onHover(null);
        }}><div className="p-y-axis">{ticks.map(t => <span className={typographyVariants({ type: "body-xs" }).base()}  key={t} style={{top: `${pointY(t, maximum)}%`}}>{formatDashboardNumber(t)}</span>)}</div><svg viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label={`${title}: ${points.length} time buckets. Exact values in View data.`}>
        {point && hover && <rect className="p-chart-time-band" data-interval={point.interval} x={bandStart} y="0" width={bandEnd - bandStart} height="88" fill="var(--accent)" fillOpacity="0.07" />}
        {ticks.map(t => <path key={t} d={`M 0 ${pointY(t, maximum)} H 100`} stroke="var(--separator)" strokeWidth="1" strokeDasharray="3 4" vectorEffect="non-scaling-stroke" />)}
        {kind === "bar" ? points.flatMap((p, i) => active.map((s, j) => {if (!known(p, s.key)) return null; const width = 65 / points.length / active.length; return <rect className="p-bar-mark" data-series={String(s.key)} key={`${i}-${String(s.key)}`} x={(i + .175) * 100 / points.length + j * width} y={pointY(value(p, s.key), maximum)} width={width * .8} height={88 - pointY(value(p, s.key), maximum)} fill={s.color} />;})) : !incomplete && active.map((s, i) => <g key={String(s.key)} transform={points.length > 1 ? `translate(${50 / points.length} 0) scale(${(points.length - 1) / points.length} 1)` : undefined}>{kind === "area" ? <path d={stackedAreaPath(points, active, i, maximum)} fill={s.color} fillOpacity="0.7" /> : <><path d={`${trendPath(points, s.key, maximum)} L 100 88 L 0 88 Z`} fill={s.color} fillOpacity="0.08" /><path d={trendPath(points, s.key, maximum)} stroke={s.color} strokeWidth="2.5" fill="none" strokeLinejoin="round" vectorEffect="non-scaling-stroke" /></>}</g>)}
        {point && hover && <path className="p-chart-time-cursor" d={`M ${x(hover.index)} 0 V 88`} stroke="var(--muted)" strokeWidth="1" strokeDasharray="3 3" vectorEffect="non-scaling-stroke" />}
      </svg></Tooltip.Trigger><Tooltip.Content className="p-chart-hover-tip" placement="top" crossOffset={hover ? (x(hover.index) / 100 - .5) * plotWidth : 0}>
        {point && <><Typography type="body-xs" color="muted" className="p-chart-hover-interval">{point.interval}</Typography><dl className="p-chart-hover-values">{active.map(s => <div key={String(s.key)}><dt className="p-series-label"><span className="p-series-dot"><svg width="8" height="8" aria-hidden="true"><circle cx="4" cy="4" r="3" fill={s.color} /></svg></span>{s.label}</dt><dd>{formatValue && known(point, s.key) ? formatValue(value(point, s.key)) : `${display(point, s.key)} ${unitOf(unit, numericValue(point, s.key))}`}</dd></div>)}</dl></>}
      </Tooltip.Content></Tooltip> : <Typography type="body-sm" className="p-chart-empty">No observations in this window.</Typography>}

      <div className="p-x-axis">{xTickIndices(points.length).map(i => <Typography type="body-xs" key={i} style={{left: `${x(i)}%`}}>{points[i].label.split(" · ").map(part => <span key={part}>{part}</span>)}</Typography>)}</div>
      <Disclosure><Disclosure.Heading><Button slot="trigger" variant="ghost" size="sm" aria-label={`View ${title} data`}><span className={typographyVariants({ type: "body-xs", color: "muted" }).base()}  >View data</span><Disclosure.Indicator /></Button></Disclosure.Heading><Disclosure.Content><Table><Table.ScrollContainer><Table.Content aria-label={`${title} data`}><Table.Header><Table.Column isRowHeader>Time</Table.Column>{series.map(s => <Table.Column key={String(s.key)}>{s.label}</Table.Column>)}</Table.Header><Table.Body>{points.map(p => <Table.Row key={p.label}><Table.Cell>{p.interval}</Table.Cell>{series.map(s => <Table.Cell key={String(s.key)}>{display(p, s.key)}</Table.Cell>)}</Table.Row>)}</Table.Body></Table.Content></Table.ScrollContainer></Table></Disclosure.Content></Disclosure>
    </Card.Content>
  </Card>;
}

export function DistributionChart({ title, data, unit }: {title: string; data: {key?: string; label: string; value: number; color: string}[]; unit: string}) {
  const total = data.reduce((n, d) => n + d.value, 0);
  const [active, setActive] = useState<number | null>(null);
  const [open, setOpen] = useState(false);
  const selected = active === null ? null : data[active];
  const percentage = (value: number) => total ? `${(value / total * 100).toFixed(1)}%` : "0.0%";
  const select = (index: number) => { setActive(index); setOpen(true); };
  return <Card role="group" aria-label={`${title} distribution`}>
    <Card.Header><Card.Title role="heading" aria-level={2}>{title}</Card.Title></Card.Header>
    <Card.Content><div className="p-distribution">
      <Tooltip delay={0} closeDelay={0} isOpen={open} onOpenChange={value => {setOpen(value); if (!value) setActive(null);}}>
        <Tooltip.Trigger className="p-donut" role="slider" aria-valuemin={1} aria-valuemax={Math.max(1, data.length)} aria-valuenow={(active ?? 0) + 1} aria-valuetext={selected ? `${selected.label}: ${selected.value} ${unit}, ${percentage(selected.value)}` : total ? `${total} ${unit}` : "No observations"} aria-label={`${title}: ${data.map(d => `${d.label} ${d.value} (${percentage(d.value)})`).join(", ")}. Use arrow keys to explore.`}
          onFocus={() => {if (data.length) select(0);}}
          onBlur={() => {setOpen(false); setActive(null);}}
          onKeyDown={event => {
            if (["ArrowRight", "ArrowDown", "ArrowLeft", "ArrowUp"].includes(event.key) && data.length) {
              event.preventDefault();
              select(((active ?? 0) + (event.key === "ArrowRight" || event.key === "ArrowDown" ? 1 : -1) + data.length) % data.length);
            }
            if (event.key === "Home" && data.length) {event.preventDefault(); select(0);}
            if (event.key === "End" && data.length) {event.preventDefault(); select(data.length - 1);}
            if (event.key === "Escape") {setOpen(false); setActive(null);}
          }}>
          <svg viewBox="0 0 120 120" aria-hidden="true">
            <circle cx="60" cy="60" r="42" stroke="var(--default)" strokeWidth="24" fill="none" />
            {total > 0 && data.map((d, i) => <circle key={d.key ?? d.label} className="p-donut-slice" data-label={d.label} cx="60" cy="60" r="42" pathLength="100" stroke={d.color}
              strokeWidth={active === i ? 28 : 24} opacity={active === null || active === i ? 1 : .45}
              strokeDasharray={`${Math.max(0, d.value / total * 100 - .8)} 100`}
              strokeDashoffset={-data.slice(0, i).reduce((n, row) => n + row.value / total * 100, 0)} transform="rotate(-90 60 60)" fill="none"
              onPointerEnter={() => select(i)} onClick={() => select(i)} />)}
          </svg>
          <div className="p-donut-total" aria-hidden="true">
            <Typography type="body" weight="semibold">{formatDashboardNumber(selected?.value ?? total)}</Typography>
          </div>
        </Tooltip.Trigger>
        <Tooltip.Content placement="top"><Typography type="body-sm">{selected?.label ?? title}</Typography><Typography type="body-xs">{(selected?.value ?? total).toLocaleString()} {unit} · {percentage(selected?.value ?? total)}</Typography></Tooltip.Content>
      </Tooltip>
    </div><div className="p-donut-legend">{data.map(d => <span className="p-series-label" key={d.key ?? d.label}><span className="p-series-dot"><svg width="8" height="8" aria-hidden="true"><circle cx="4" cy="4" r="3" fill={d.color} /></svg></span><span className={typographyVariants({ type: "body-xs", color: "muted" }).base()}  >{d.label}</span></span>)}</div></Card.Content>
  </Card>;
}
