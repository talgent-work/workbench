import { useState } from "react";
import { Button, Tooltip } from "@heroui/react";
import { useQuery } from "@tanstack/react-query";
import { UnifiedMessageKind } from "@talgent/contracts/dispatcher/public/v1/unified_message_pb";
import { streamClient } from "../../../lib/client";
import { formatCompactTokenCount } from "../../../lib/trace-view-model";
import { tokenCategories, tokenTotal } from "../../../lib/token-usage";
import { turnAxis, type Call, type ModelCall, type TimeRange, type ToolCall, type Turn } from "./turns";
import { formatClock, formatMillis } from "./work-status";

const shownCalls = 40;
const legend = [["model", "Model call"], ["tool", "Tool"], ["mcp", "MCP tool"], ["agent", "Subagent"], ["wait", "Waited for you"]] as const;
const visibleWaitMillis = 1000;

/** One Turn's calls on an axis covering only that Turn. */
export function TurnTimeline({ turn, workId, now }: { turn: Turn; workId: string; now: number }) {
  const [showAll, setShowAll] = useState(false);
  const range = { start: turn.start, end: turn.open ? Math.max(turn.end, now) : turn.end };
  const axis = turnAxis(range, turn.calls);
  const calls = showAll ? turn.calls : turn.calls.slice(0, shownCalls);
  // The legend names only the kinds this Turn's bars use.
  const kinds = new Set<string>(calls.flatMap(call => call.kind === "model" ? ["model"] : [call.family, ...(call.wait && call.wait.end - call.wait.start >= visibleWaitMillis ? ["wait"] : [])]));
  if (!turn.calls.length) return <p className="wu-empty">No calls recorded in this Turn.</p>;
  return <div className="wu-timeline">
    <ul className="wu-legend" aria-label="Bar colours">
      {legend.filter(([kind]) => kinds.has(kind)).map(([kind, label]) => <li key={kind}><i className="wu-bar-swatch" data-kind={kind} />{label}</li>)}
    </ul>
    <div className="wu-axis" aria-hidden="true">
      <span>{formatClock(range.start)}</span><span>{formatClock(range.end)}</span>
      {axis.breaks.map(at => <i key={at} className="wu-break" style={{ left: `${at}%` }} />)}
    </div>
    <ol className="wu-calls" aria-label="Calls in this Turn">
      {calls.map(call => <li key={call.key}><CallRow call={call} workId={workId} bar={segment => ({ left: `${axis.position(segment.start)}%`, width: `${Math.max(axis.position(segment.end) - axis.position(segment.start), .6)}%` })} /></li>)}
    </ol>
    {turn.calls.length > calls.length && <Button size="sm" variant="ghost" onPress={() => setShowAll(true)}>Show {turn.calls.length - calls.length} more calls</Button>}
  </div>;
}

function CallRow({ call, workId, bar }: { call: Call; workId: string; bar: (range: TimeRange) => { left: string; width: string } }) {
  const [open, setOpen] = useState(false);
  const label = call.kind === "model" ? "Model call" : call.name;
  const waited = call.kind === "tool" && call.wait && call.wait.end - call.wait.start >= visibleWaitMillis ? call.wait : null;
  const metric = call.kind === "model" ? formatCompactTokenCount(tokenTotal(call.usage)) : formatMillis(call.end - call.start);
  return <Tooltip delay={0} closeDelay={100} isOpen={open} onOpenChange={setOpen}>
    <Button variant="ghost" className="wu-call" aria-label={`${label}${call.kind === "tool" && call.source ? ` (${call.source})` : ""}, ${metric}`} onPress={() => setOpen(true)}>
      <span className="wu-call-name"><span>{label}</span>{call.kind === "tool" && call.source && <span className="wu-muted">{call.source}</span>}</span>
      <span className="wu-track" aria-hidden="true">
        {waited && <span className="wu-bar" data-kind="wait" style={bar(waited)} />}
        <span className="wu-bar" data-kind={call.kind === "tool" ? call.family : "model"} style={bar(call.kind === "tool" && call.run ? call.run : call)} />
      </span>
      <span className="wu-call-metric">{waited ? <>Waited for you {formatMillis(waited.end - waited.start)}</> : metric}</span>
    </Button>
    <Tooltip.Content placement="bottom" className="wu-card">
      {call.kind === "model" ? <ModelCallCard call={call} /> : <ToolCallCard call={call} workId={workId} waited={waited} enabled={open} />}
    </Tooltip.Content>
  </Tooltip>;
}

function ModelCallCard({ call }: { call: ModelCall }) {
  return <>
    <p className="wu-card-title">Model call</p>
    <p className="wu-muted">{call.model} · {formatClock(call.start)}</p>
    <dl className="wu-facts">
      <Fact label="Duration" value={formatMillis(call.end - call.start)} />
      {call.ttftMs !== null && <Fact label="First token" value={formatMillis(call.ttftMs)} />}
      {call.stopReason && <Fact label="Stop reason" value={call.stopReason} />}
    </dl>
    <dl className="wu-facts">{tokenCategories.map(category => <Fact key={category.key} label={category.label} value={call.usage[category.key].toLocaleString()} />)}</dl>
  </>;
}

function ToolCallCard({ call, workId, waited, enabled }: { call: ToolCall; workId: string; waited: TimeRange | null; enabled: boolean }) {
  // The call's summary is the message stream's, found by the call's identity.
  const summary = useQuery({
    queryKey: ["tool-call-summary", workId, call.callId],
    queryFn: async ({ signal }) => {
      const message = (await streamClient.listMessagesBySession({ agentWorkId: workId, toolCallIds: [call.callId], kinds: [UnifiedMessageKind.ACTION], limit: 1 }, { signal })).messages[0];
      return message?.payload.case === "action" ? message.payload.value.summary : "";
    },
    enabled: enabled && Boolean(call.callId),
    retry: false,
    staleTime: Infinity,
  });
  return <>
    <p className="wu-card-title">{call.name}</p>
    <p className="wu-muted">{[call.source, formatClock(call.start)].filter(Boolean).join(" · ")}</p>
    {summary.data && <p className="wu-summary">{summary.data}</p>}
    <dl className="wu-facts">
      {waited && <Fact label="Waited for you" value={formatMillis(waited.end - waited.start)} />}
      <Fact label="Ran" value={formatMillis(call.run ? call.run.end - call.run.start : call.end - call.start)} />
      {call.result && <Fact label="Result" value={call.result === "succeeded" ? "Succeeded" : "Failed"} />}
    </dl>
  </>;
}

function Fact({ label, value }: { label: string; value: string }) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>;
}
