import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { Alert, Button, Chip, Disclosure, Skeleton, Tooltip } from "@heroui/react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Download, RefreshCw } from "lucide-react";
import type { Span } from "@talgent/workbench-contracts/trace/public/v1/trace_pb";
import { traceClient } from "../../../lib/client";
import { formatCompactTokenCount, traceMetricKinds } from "../../../lib/trace-view-model";
import { cacheHitRate, formatHitRate, tokenCategories, tokenTotal, tokenUsageFromMeasurements } from "../../../lib/token-usage";
import { workMetricsWindow } from "../../../lib/use-work-trace";
import { workPresentation } from "../../../lib/work-state-presentation";
import { buildTurns, spanRange, toolName, type Turn } from "./turns";
import { TurnTimeline } from "./turn-timeline";
import { formatAgo, formatClock, formatMillis, type WorkUsageStatus } from "./work-status";
import "./work-usage.css";

const refreshMillis = 10_000;
const pageSize = 500;

/** A Work's Token Usage: its totals, whether it is working, and its Turns
 * newest first. Native evidence stays out of view; the download keeps it.
 * `status` is the Work's state as the caller may read it; `projectId` is
 * empty for a Work without a Project. */
export function WorkUsageReader({ projectId, agentWorkId, createdAt = null, status, toolbar }: {
  projectId: string; agentWorkId: string; createdAt?: Date | null; status: WorkUsageStatus | null; toolbar?: HTMLElement | null;
}) {
  const live = status?.state === "working" || status?.state === "starting";
  const now = useNow(refreshMillis);
  const spans = useInfiniteQuery({
    queryKey: ["work-usage-spans", projectId, agentWorkId], initialPageParam: "",
    queryFn: ({ pageParam, signal }) => traceClient.listSpansBySession({ projectId, agentWorkId, limit: pageSize, afterCursor: pageParam, newestFirst: true }, { signal }),
    getNextPageParam: page => page.nextCursor || undefined,
    refetchInterval: live ? refreshMillis : false, retry: false, refetchOnWindowFocus: false,
  });
  const metrics = useQuery({
    queryKey: ["trace-metrics", projectId, agentWorkId, createdAt?.getTime() ?? 0],
    queryFn: ({ signal }) => {
      const range = workMetricsWindow(new Date(), createdAt);
      return traceClient.getSessionTraceMetrics({ projectId, agentWorkId, startTime: timestampFromDate(range.start), endTime: timestampFromDate(range.end), stepSeconds: range.stepSeconds, metricKinds: [...traceMetricKinds] }, { signal });
    },
    refetchInterval: live ? refreshMillis : false, retry: false, refetchOnWindowFocus: false,
  });
  const loaded = useMemo(() => spans.data?.pages.flatMap(page => page.spans) ?? [], [spans.data]);
  const turns = useMemo(() => buildTurns(loaded, Boolean(spans.hasNextPage)), [loaded, spans.hasNextPage]);
  const refresh = () => { void spans.refetch(); void metrics.refetch(); };
  const download = () => {
    const body = JSON.stringify({ agentWorkId, spans: loaded, hasEarlierSpans: Boolean(spans.hasNextPage), metrics: metrics.data ?? null }, (_, value) => typeof value === "bigint" ? value.toString() : value, 2);
    const url = URL.createObjectURL(new Blob([body], { type: "application/json" }));
    const link = document.createElement("a"); link.href = url; link.download = `${agentWorkId}-usage.json`; link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  return <div className="pr-document wu-reader">
    {toolbar && createPortal(<>
      <Tooltip><Button isIconOnly size="sm" variant="ghost" aria-label="Refresh usage" isPending={spans.isFetching || metrics.isFetching} onPress={refresh}><RefreshCw size={16} /></Button><Tooltip.Content>Refresh usage</Tooltip.Content></Tooltip>
      <Tooltip><Button isIconOnly size="sm" variant="ghost" aria-label="Download usage details" isDisabled={!spans.data && !metrics.data} onPress={download}><Download size={16} /></Button><Tooltip.Content>Download usage details</Tooltip.Content></Tooltip>
    </>, toolbar)}
    {metrics.isPending ? <Skeleton className="h-16 rounded-lg" aria-label="Loading token usage" />
      : metrics.isError && !metrics.data ? <QueryError title="Token usage couldn't load." onRetry={() => void metrics.refetch()} />
      : <UsageTotals totals={metrics.data?.totals ?? []} />}
    <StatusLine status={status} spans={loaded} now={now} />
    <h2 className="pr-section-title">Turns</h2>
    {spans.isPending ? <Skeleton className="h-24 rounded-lg" aria-label="Loading Turns" />
      : spans.isError && !spans.data ? <QueryError title="Turns couldn't load." onRetry={() => void spans.refetch()} />
      : turns.length ? <ol className="wu-turns">{turns.map((turn, index) => <li key={turn.key}><TurnRow turn={turn} running={turn.open && live} defaultExpanded={index === 0} workId={agentWorkId} now={now} /></li>)}</ol>
      : !spans.hasNextPage && <p className="wu-empty">No Turns to show.</p>}
    {spans.hasNextPage && <Button size="sm" variant="secondary" className="wu-earlier" isPending={spans.isFetchingNextPage} onPress={() => void spans.fetchNextPage()}>Load earlier turns</Button>}
  </div>;
}

function UsageTotals({ totals }: { totals: Array<{ metricKind: string; value: number }> }) {
  const usage = tokenUsageFromMeasurements(totals);
  const rate = cacheHitRate(usage);
  return <section aria-label="Token usage"><dl className="pr-metrics wu-totals">
    <div><dt>Total</dt><dd>{formatCompactTokenCount(tokenTotal(usage))}</dd></div>
    {tokenCategories.map(category => <div key={category.key}><dt>{category.label}</dt><dd>{formatCompactTokenCount(usage[category.key])}</dd></div>)}
    {rate !== null && <div><dt>Cache hit rate</dt><dd>{formatHitRate(rate)}</dd></div>}
  </dl></section>;
}

/** Whether the Work is working comes from its state; trace activity only adds
 * what it did last and when. */
function StatusLine({ status, spans, now }: { status: WorkUsageStatus | null; spans: Span[]; now: number }) {
  const latest = spans.filter(span => span.name === "claude_code.llm_request" || span.name === "claude_code.tool")
    .map(span => ({ span, end: spanRange(span).end })).sort((left, right) => right.end - left.end)[0];
  const presentation = workPresentation(status ?? undefined);
  const stopped = status && ["stopped", "failed", "closed"].includes(status.state) && status.updatedAt;
  const activity = latest && <span>{latest.span.name === "claude_code.llm_request" ? `Model call by ${latest.span.attributes.model || "an unknown model"}` : toolName(latest.span.attributes).name} finished {formatClock(latest.end, now)} · {formatAgo(now - latest.end)}</span>;
  return <p className="wu-status" role="status">
    {presentation && <Chip size="sm" variant="soft" color={presentation.color}><Chip.Label>{presentation.label}</Chip.Label></Chip>}
    {stopped ? <span>at {formatClock(stopped.getTime(), now)}</span> : status?.waiting ? null : activity}
  </p>;
}

function TurnRow({ turn, running, defaultExpanded, workId, now }: { turn: Turn; running: boolean; defaultExpanded: boolean; workId: string; now: number }) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  return <Disclosure isExpanded={expanded} onExpandedChange={setExpanded}>
    <Disclosure.Heading>
      <Button slot="trigger" variant="ghost" className="wu-turn">
        <Disclosure.Indicator />
        <span className="wu-turn-time"><span className="sr-only">Turn started </span>{formatClock(turn.start, now)}</span>
        <span className="wu-turn-duration">{running ? <Chip size="sm" variant="soft" color="success"><Chip.Label>Running</Chip.Label></Chip> : formatMillis(turn.end - turn.start)}</span>
        <span className="wu-turn-tokens">{formatCompactTokenCount(tokenTotal(turn.usage))}<span className="sr-only"> tokens</span></span>
      </Button>
    </Disclosure.Heading>
    <Disclosure.Content>{expanded && <TurnTimeline turn={turn} workId={workId} now={now} />}</Disclosure.Content>
  </Disclosure>;
}

function QueryError({ title, onRetry }: { title: string; onRetry: () => void }) {
  return <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Title>{title}</Alert.Title></Alert.Content><Button size="sm" variant="secondary" onPress={onRetry}>Retry</Button></Alert>;
}

function useNow(intervalMillis: number) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMillis);
    return () => clearInterval(timer);
  }, [intervalMillis]);
  return now;
}
