import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Span } from "@talgent/workbench-contracts/trace/public/v1/trace_pb";
import { addTokenUsage, emptyTokenUsage, tokenUsageFromAttributes, type TokenUsage } from "../../../lib/token-usage";

export type TimeRange = { start: number; end: number };

export type ModelCall = TimeRange & {
  kind: "model"; key: string; model: string; ttftMs: number | null; stopReason: string; usage: TokenUsage;
};

/** What kind of tool a call used, which its bar's colour tells apart. */
export type ToolFamily = "tool" | "mcp" | "agent";

export type ToolCall = TimeRange & {
  kind: "tool"; key: string; callId: string; name: string; source: string; family: ToolFamily;
  /** Time spent waiting on the person, from the blocked-on-user evidence. */
  wait: TimeRange | null;
  run: TimeRange | null;
  result: "succeeded" | "failed" | null;
};

export type Call = ModelCall | ToolCall;

export type Turn = TimeRange & {
  key: string;
  /** No interaction span yet: the newest Turn, still running or cut off. */
  open: boolean;
  /** Every model call in the Turn, delegated ones included. */
  usage: TokenUsage;
  /** The Turn's own calls, oldest first. */
  calls: Call[];
};

const interaction = "claude_code.interaction";
const modelRequest = "claude_code.llm_request";
const toolCall = "claude_code.tool";
const waitSpan = "claude_code.tool.blocked_on_user";
const executionSpan = "claude_code.tool.execution";

const keyOf = (span: Span) => `${span.traceId}:${span.spanId}`;
const parentKeyOf = (span: Span) => `${span.traceId}:${span.parentSpanId}`;

export function spanRange(span: Span): TimeRange {
  const start = span.startTime ? timestampDate(span.startTime).getTime() : 0;
  return { start, end: start + Number(span.durationNanos) / 1_000_000 };
}

/**
 * Groups loaded spans into Turns, newest first. A Turn is an interaction span
 * and its descendants. Claude Code exports the interaction span only when the
 * Turn ends, so the newest calls without one form an open Turn. Older calls
 * without one belong to a Turn on a page not loaded yet; they are left out
 * until it is, unless no earlier page remains.
 */
export function buildTurns(spans: Span[], hasEarlier: boolean): Turn[] {
  const byKey = new Map(spans.map(span => [keyOf(span), span]));
  const groups = new Map<string, { root: Span | null; spans: Span[] }>();
  const rootCache = new Map<string, string>();
  const rootOf = (span: Span, seen = new Set<string>()): string => {
    const key = keyOf(span);
    const cached = rootCache.get(key);
    if (cached) return cached;
    const parent = byKey.get(parentKeyOf(span));
    const root = span.name === interaction ? key : parent && !seen.has(key) ? rootOf(parent, seen.add(key)) : parentKeyOf(span);
    rootCache.set(key, root);
    return root;
  };
  for (const span of byKey.values()) {
    const root = rootOf(span);
    const group = groups.get(root) ?? { root: null, spans: [] };
    if (keyOf(span) === root) group.root = span;
    else group.spans.push(span);
    groups.set(root, group);
  }

  const newestInteraction = Math.max(...[...groups.values()].flatMap(group => group.root ? [spanRange(group.root).start] : []));
  const rangeOf = (group: { root: Span | null; spans: Span[] }) => {
    if (group.root) return spanRange(group.root);
    const ranges = group.spans.map(spanRange);
    return { start: Math.min(...ranges.map(item => item.start)), end: Math.max(...ranges.map(item => item.end)) };
  };
  // Only the newest group without an interaction span, after every finished Turn, can be the open one.
  const openKey = [...groups].filter(([, group]) => !group.root && rangeOf(group).start > newestInteraction)
    .sort((left, right) => rangeOf(right[1]).start - rangeOf(left[1]).start)[0]?.[0];
  const turns: Turn[] = [];
  for (const [rootKey, group] of groups) {
    const range = rangeOf(group);
    const open = rootKey === openKey;
    if (!group.root && !open && hasEarlier) continue;
    turns.push({ key: rootKey, ...range, open, usage: turnUsage(group.spans), calls: turnCalls(rootKey, group.spans) });
  }
  return turns.sort((left, right) => right.start - left.start);
}

function turnUsage(spans: Span[]): TokenUsage {
  return spans.filter(span => span.name === modelRequest).reduce((usage, span) => addTokenUsage(usage, tokenUsageFromAttributes(span.attributes)), emptyTokenUsage());
}

function turnCalls(rootKey: string, spans: Span[]): Call[] {
  const children = new Map<string, Span[]>();
  for (const span of spans) children.set(parentKeyOf(span), [...(children.get(parentKeyOf(span)) ?? []), span]);
  const calls: Call[] = [];
  for (const span of children.get(rootKey) ?? []) {
    const range = spanRange(span);
    if (span.name === modelRequest) {
      const ttft = Number(span.attributes.ttft_ms);
      calls.push({ kind: "model", key: keyOf(span), ...range, model: span.attributes.model || "Unknown model", ttftMs: span.attributes.ttft_ms && Number.isFinite(ttft) ? ttft : null, stopReason: span.attributes.stop_reason ?? "", usage: tokenUsageFromAttributes(span.attributes) });
    }
    if (span.name === toolCall) {
      // One call is one row: its wait and execution are its child spans.
      const parts = children.get(keyOf(span)) ?? [];
      const wait = parts.find(part => part.name === waitSpan);
      const execution = parts.find(part => part.name === executionSpan);
      const success = execution?.attributes.success;
      calls.push({
        kind: "tool", key: keyOf(span), ...range, callId: span.callId || span.attributes.tool_use_id || "",
        ...toolName(span.attributes),
        wait: wait ? spanRange(wait) : null, run: execution ? spanRange(execution) : null,
        result: success === "true" ? "succeeded" : success === "false" || execution?.attributes.error ? "failed" : null,
      });
    }
  }
  return calls.sort((left, right) => left.start - right.start);
}

/** MCP tools read by their own name with their server as the source. */
export function toolName(attributes: Record<string, string>): { name: string; source: string; family: ToolFamily } {
  const tool = attributes.tool_name || "Unknown tool";
  const mcp = /^mcp__(.+?)__(.+)$/.exec(tool);
  if (mcp) return { name: mcp[2], source: mcp[1], family: "mcp" };
  if (tool === "mcp_tool") return { name: attributes.mcp_tool_name || "MCP tool", source: attributes.mcp_server_name ?? "", family: "mcp" };
  if (tool === "Skill") return { name: "Skill", source: attributes.skill_name ?? "", family: "tool" };
  if (tool === "Agent" || tool === "Task") return { name: tool, source: attributes.subagent_type ?? "", family: "agent" };
  return { name: tool, source: "", family: "tool" };
}

/** Waits longer than this fold into an axis break. */
export const foldedWaitMillis = 60_000;
const breakWidth = 3;

export type TurnAxis = { position: (time: number) => number; breaks: number[] };

/** A Turn's time axis in percent, with each long wait folded to a short break
 * so that it does not flatten every other call. */
export function turnAxis(turn: TimeRange, calls: Call[]): TurnAxis {
  const folds = calls.flatMap(call => call.kind === "tool" && call.wait && call.wait.end - call.wait.start > foldedWaitMillis ? [call.wait] : [])
    .sort((left, right) => left.start - right.start)
    .reduce<TimeRange[]>((merged, wait) => {
      const last = merged.at(-1);
      if (last && wait.start <= last.end) last.end = Math.max(last.end, wait.end);
      else merged.push({ ...wait });
      return merged;
    }, []);
  const folded = folds.reduce((sum, fold) => sum + fold.end - fold.start, 0);
  const visible = Math.max(turn.end - turn.start - folded, 1);
  const scale = (100 - folds.length * breakWidth) / visible;
  const position = (time: number) => {
    const at = Math.min(Math.max(time, turn.start), turn.end);
    let offset = 0;
    let elapsed = at - turn.start;
    for (const fold of folds) {
      if (at <= fold.start) break;
      const inside = Math.min(at, fold.end) - fold.start;
      elapsed -= inside;
      offset += breakWidth * inside / (fold.end - fold.start);
    }
    return Math.min(100, elapsed * scale + offset);
  };
  return { position, breaks: folds.map(fold => position(fold.start) + breakWidth / 2) };
}
