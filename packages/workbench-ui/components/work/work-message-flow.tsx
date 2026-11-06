import { Fragment, memo, useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Button, Tooltip } from "@heroui/react";
import { ArrowDown, Check, ChevronDown, Copy, FilePenLine, FileSearch, Search, Terminal, Wrench } from "lucide-react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { ContentBlockType, MessageDetailFormat, ResultStatus, UnifiedMessageKind, type UnifiedMessage } from "@talgent/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import { Markdown } from "../markdown";
import { ToolMessageDetail } from "./tool-message-detail";
import { ToolReadingContent } from "./tool-reading-content";
import { WorkQuestionRecord } from "./work-question-record";
import type { useWorkConversation } from "../../hooks/use-work-conversation";
import type { WorkTurn } from "../../lib/work-conversation";
import "./work-message-flow.css";

export interface ToolPreview {
  workId: string;
  toolCallId: string;
  title: string;
}

type Conversation = ReturnType<typeof useWorkConversation>;
type OpenTool = (tool: ToolPreview, trigger: HTMLElement) => void;

export function WorkMessageFlow({ conversation, currentTurnId, waiting, onOpenTool }: {
  conversation: Conversation; currentTurnId?: string; waiting: boolean; onOpenTool: OpenTool;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const follow = useRef(true);
  const hold = useRef(false);
  const anchored = useRef<string | undefined>(undefined);
  // The first visible message when a page was requested, kept at its current
  // place while the request runs, and the window it was taken from: the page
  // lands only in a later window, and the reader keeps any scrolling since.
  const scrollAnchor = useRef<{ id: string; top: number; turns: Conversation["turns"] } | null>(null);
  const lastTop = useRef(0);
  const touchY = useRef<number | null>(null);
  const frame = useRef(0);
  const [activeTurn, setActiveTurn] = useState("");
  // Away from the latest messages without browsing history pages.
  const [away, setAway] = useState(false);
  // Keep at most one inline output reader mounted across the whole window.
  const [detailId, setDetailId] = useState<string | null>(null);
  const navigationTurns = conversation.navigationTurns;
  const navigation = useRef(navigationTurns);
  useEffect(() => { navigation.current = navigationTurns; });
  const pendingTurn = useRef<string | null>(null);
  // Locating the current turn reads every anchor's geometry: once a frame.
  const measure = useCallback((element: HTMLElement) => {
    if (frame.current) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = 0;
      const line = element.getBoundingClientRect().top + element.clientHeight / 3 + 1;
      const users = navigation.current.map(user => document.getElementById(`wm-user-${user.id}`)).filter((user): user is HTMLElement => user !== null);
      setActiveTurn((users.filter(user => user.getBoundingClientRect().top <= line).at(-1) ?? users[0])?.id ?? "");
      // A view placed on purpose, at the user's new message or a chosen turn, is not away.
      setAway(!follow.current && !hold.current && element.scrollHeight - element.scrollTop - element.clientHeight > element.clientHeight / 3);
    });
  }, []);
  const scrollToTurn = useCallback((id: string) => {
    const element = scroller.current, target = document.getElementById(`wm-user-${id}`);
    if (!element || !target) return false;
    follow.current = false; hold.current = true;
    target.querySelector<HTMLElement>("p")?.focus({ preventScroll: true });
    element.scrollTo({ top: element.scrollTop + target.getBoundingClientRect().top - element.getBoundingClientRect().top - 24, behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth" });
    setActiveTurn(target.id);
    return true;
  }, []);
  useLayoutEffect(() => {
    const element = scroller.current;
    if (!element) return;
    element.style.setProperty("--wm-viewport-height", `${element.clientHeight}px`);
    if (pendingTurn.current) {
      if (!conversation.loading && scrollToTurn(pendingTurn.current)) pendingTurn.current = null;
      return;
    }
    if (conversation.newUserId && anchored.current !== conversation.newUserId) {
      const target = document.getElementById(`wm-user-${conversation.newUserId}`);
      if (target) {
        follow.current = false; hold.current = true; anchored.current = conversation.newUserId;
        element.scrollTop += target.getBoundingClientRect().top - element.getBoundingClientRect().top - element.clientHeight / 3;
      }
    } else if (scrollAnchor.current && !conversation.loading && scrollAnchor.current.turns !== conversation.turns) {
      const target = document.getElementById(scrollAnchor.current.id);
      if (target) element.scrollTop += target.getBoundingClientRect().top - scrollAnchor.current.top;
      scrollAnchor.current = null;
    } else if (follow.current) element.scrollTop = element.scrollHeight;
    measure(element);
  }, [conversation.turns, conversation.newUserId, conversation.loading, scrollToTurn, measure]);
  useEffect(() => {
    const element = scroller.current;
    if (!element) return;
    const dock = element.parentElement?.querySelector<HTMLElement>(".wi-dock");
    const content = element.firstElementChild;
    // Content can also grow after it renders (highlighted code, diagrams,
    // images) and the dock with a request card: a followed view stays at the end.
    const observer = new ResizeObserver(() => {
      element.style.setProperty("--wm-viewport-height", `${element.clientHeight}px`);
      element.parentElement?.style.setProperty("--wm-dock-height", `${dock?.offsetHeight ?? 0}px`);
      if (follow.current) element.scrollTop = element.scrollHeight;
    });
    observer.observe(element);
    if (dock) observer.observe(dock);
    if (content) observer.observe(content);
    return () => { observer.disconnect(); cancelAnimationFrame(frame.current); };
  }, []);
  // Reading upward, by any input, releases following at once: a message
  // arriving before the view has moved far must not pull the reader back.
  const leaveLatest = () => { hold.current = false; follow.current = false; };
  const loadPage = (direction: "older" | "newer") => {
    const element = scroller.current;
    if (!element || conversation.loading || !(direction === "older" ? conversation.hasMore : conversation.hasLaterMessages)) return;
    const top = element.getBoundingClientRect().top;
    const visible = [...element.querySelectorAll<HTMLElement>("[data-message-id]")].find(item => item.getBoundingClientRect().bottom > top);
    if (visible) scrollAnchor.current = { id: visible.id, top: visible.getBoundingClientRect().top, turns: conversation.turns };
    follow.current = false;
    if (direction === "older") conversation.loadOlder();
    else conversation.loadNewer();
  };
  return <>
    <div className="wm-scroll p-fill" ref={scroller} onWheel={event => { if (event.deltaY < 0) leaveLatest(); else hold.current = false; }}
      onTouchStart={event => { touchY.current = event.touches[0]?.clientY ?? null; }}
      onTouchMove={event => {
        const y = event.touches[0]?.clientY;
        if (y !== undefined && touchY.current !== null && y > touchY.current) leaveLatest(); else hold.current = false;
        touchY.current = y ?? null;
      }}
      onKeyDown={event => {
        if (["ArrowUp", "PageUp", "Home"].includes(event.key) || (event.key === " " && event.shiftKey)) leaveLatest();
        else if (["ArrowDown", "PageDown", "End", " "].includes(event.key)) hold.current = false;
      }} onScroll={event => {
    const element = event.currentTarget;
    const distance = element.scrollHeight - element.scrollTop - element.clientHeight;
    const upward = element.scrollTop < lastTop.current - 1;
    lastTop.current = element.scrollTop;
    // At the very end the view follows again; moving upward near it does not.
    follow.current = !hold.current && !conversation.browsingHistory && (distance <= 2 || (!upward && distance < 80));
    const anchor = scrollAnchor.current && document.getElementById(scrollAnchor.current.id);
    if (anchor) scrollAnchor.current!.top = anchor.getBoundingClientRect().top;
    measure(element);
    if (hold.current || conversation.error) return;
    if (element.scrollTop < 100) loadPage("older");
    else if (distance < 100) loadPage("newer");
  }}>
    <div className="wm-conversation">
      {conversation.loading && <p className="sr-only" role="status">Loading messages…</p>}
      {conversation.turns.map(turn => <TurnView key={turn.key} turn={turn} active={Boolean(turn.turnId && turn.turnId === currentTurnId)} waiting={waiting} anchoredId={conversation.newUserId} detailId={detailId} onDetail={setDetailId} onOpenTool={onOpenTool} />)}
    </div>
  </div>
    {(conversation.browsingHistory || away) && <Tooltip><Button className="wm-latest" size="sm" variant="secondary" isIconOnly aria-label={conversation.hasNewer ? "New messages · Back to latest" : "Back to latest"} onPress={() => {
      follow.current = true; hold.current = false; scrollAnchor.current = null; pendingTurn.current = null; setAway(false);
      // The latest messages are already in this window unless browsing history.
      if (conversation.browsingHistory) conversation.showLatest();
      else if (scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
    }}><ArrowDown size={17} aria-hidden="true" /></Button><Tooltip.Content>{conversation.hasNewer ? "New messages · Back to latest" : "Back to latest"}</Tooltip.Content></Tooltip>}
    {(navigationTurns.length > 0 || conversation.indexError) && <nav className="wm-toc" aria-label="Conversation turns">
      {(conversation.hasOlderTurns || conversation.indexError) && <Button variant="ghost" className="wm-toc-item" isDisabled={conversation.indexLoading} aria-label={conversation.indexError ? "Retry turn navigation" : "Load earlier turns"} onPress={conversation.indexError ? conversation.retryIndex : conversation.loadOlderTurns}><span className="wm-toc-label">{conversation.indexError ? "Retry" : "Earlier turns"}</span><ChevronDown size={12} className="wm-chevron-open" /></Button>}
      {navigationTurns.map((user, index) => <Button key={user.id} variant="ghost" className="wm-toc-item" aria-label={`Turn ${index + 1}: ${user.title}`} aria-controls={`wm-user-${user.id}`} aria-current={activeTurn === `wm-user-${user.id}` ? "location" : undefined} onPress={() => {
        scrollAnchor.current = null;
        if (scrollToTurn(user.id)) { pendingTurn.current = null; return; }
        follow.current = false; hold.current = true; pendingTurn.current = user.id;
        conversation.showTurn(user);
      }}><span className="wm-toc-label"><span className="wm-toc-number">{index + 1}</span>{user.title}</span><span className="wm-toc-mark" aria-hidden="true" /></Button>)}
    </nav>}
  </>;
}

const TurnView = memo(function TurnView({ turn, active, waiting, anchoredId, detailId, onDetail, onOpenTool }: {
  turn: WorkTurn; active: boolean; waiting: boolean; anchoredId?: string; detailId: string | null;
  onDetail: (id: string | null) => void; onOpenTool: OpenTool;
}) {
  const users = turn.messages.filter(message => message.kind === UnifiedMessageKind.USER);
  const results = turn.messages.filter(message => message.payload.case === "result");
  const lastResult = results.at(-1)?.payload;
  const result = lastResult?.case === "result" ? lastResult.value : undefined;
  const running = active && !result;
  // Each Steer, input sent while the Turn ran, starts a segment of the Turn.
  // A Turn a background task started has no user message before its work.
  const segments: { user?: UnifiedMessage; process: UnifiedMessage[] }[] = [{ process: [] }];
  for (const message of turn.messages) {
    if (message.payload.case === "result") continue;
    const current = segments.at(-1)!;
    if (message.kind !== UnifiedMessageKind.USER) current.process.push(message);
    else if (!current.user && !current.process.length) current.user = message;
    else segments.push({ user: message, process: [] });
  }
  // Errors without turn attribution stay visible without inventing a turn.
  if (!turn.turnId && !users.length) return <article className="wm-turn">{segments[0].process.map(message => <ProcessMessage key={message.id} message={message} detailId={detailId} onDetail={onDetail} />)}{results.map(message => <Result key={message.id} message={message} />)}</article>;
  const start = turn.messages[0]?.timestamp;
  const seconds = Math.max(0, Math.floor((result ? Number(result.durationMs) : 0) / 1000));
  const label: ReactNode = result ? result.status === ResultStatus.SUCCESS ? seconds ? `Worked for ${duration(seconds)}` : "View work" : result.status === ResultStatus.FAILED ? "Work failed" : result.status === ResultStatus.TIMED_OUT ? "Work timed out" : result.status === ResultStatus.REQUIRES_INPUT ? "Waiting for input" : `Stopped after ${duration(seconds)}`
    : running ? waiting ? "Waiting for your response" : <WorkingFor since={start ? timestampDate(start).getTime() : undefined} /> : "View work";
  return <article className="wm-turn" data-turn-id={turn.turnId} data-new-user-anchor={users.some(user => user.id === anchoredId) || undefined}>
    {segments.map((segment, index) => {
      const last = index === segments.length - 1;
      const next = segments[index + 1]?.user?.timestamp;
      const from = segment.user?.timestamp ?? segment.process[0]?.timestamp;
      const worked = next && from ? Math.max(0, Math.floor((timestampDate(next).getTime() - timestampDate(from).getTime()) / 1000)) : 0;
      return <Fragment key={segment.user?.id ?? "process"}>
        {segment.user && <UserMessage user={segment.user} />}
        <Segment messages={segment.process} running={last && running} waiting={waiting} settled={!last || result?.status === ResultStatus.SUCCESS} shown={last ? Boolean(segment.process.length || running || result) : segment.process.length > 0}
          label={last ? label : `Worked for ${duration(worked)} before your update`} detailId={detailId} onDetail={onDetail} onOpenTool={onOpenTool} />
      </Fragment>;
    })}
    {results.map(message => <Result key={message.id} message={message} />)}
  </article>;
});

// The running clock ticks alone, so a long Turn is not rendered every second.
function WorkingFor({ since }: { since?: number }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  return <>{`Working for ${duration(Math.max(0, Math.floor((since === undefined ? 0 : now - since) / 1000)))}`}</>;
}

function duration(seconds: number) {
  return seconds >= 60 ? `${Math.floor(seconds / 60)}m ${seconds % 60}s` : `${seconds}s`;
}

function UserMessage({ user }: { user: UnifiedMessage }) {
  return <div className="wm-user" id={`wm-user-${user.id}`} data-message-id={user.id}><p tabIndex={-1}>{user.payload.case === "message" && user.payload.value.content.map((block, index) =>
    block.type === ContentBlockType.TEXT ? <Fragment key={index}>{index > 0 ? "\n\n" : ""}{block.text}</Fragment>
      : block.type === ContentBlockType.IMAGE ? /^(image\/(png|jpeg|gif|webp))$/.test(block.mimeType)
        ? <img key={index} src={`data:${block.mimeType};base64,${block.text}`} alt="Attached image" loading="lazy" decoding="async" />
        : <span key={index}>Image unavailable</span>
      : null,
  )}</p></div>;
}

// A segment's process is collapsed once settled: a successful Turn's last
// segment, or any segment a later Steer followed. Otherwise it stays open.
function Segment({ messages, running, waiting, settled, shown, label, detailId, onDetail, onOpenTool }: {
  messages: UnifiedMessage[]; running: boolean; waiting: boolean; settled: boolean; shown: boolean; label: ReactNode;
  detailId: string | null; onDetail: (id: string | null) => void; onOpenTool: OpenTool;
}) {
  const processId = useId();
  const phase = settled ? "completed" : "process";
  const [expanded, setExpanded] = useState({ process: true, completed: false });
  const open = expanded[phase];
  if (!shown) return null;
  return <>
    <Button variant="ghost" className="wm-process-toggle" aria-expanded={open} aria-controls={processId} onPress={() => setExpanded(value => ({ ...value, [phase]: !open }))}><span>{label}</span><ChevronDown size={15} className={open ? "wm-chevron-open" : ""} aria-hidden="true" /></Button>
    <div className="wm-process" id={processId} hidden={!open}>{open && <Process messages={messages} running={running} waiting={waiting} detailId={detailId} onDetail={onDetail} onOpenTool={onOpenTool} />}</div>
  </>;
}

function Process({ messages, running, waiting, detailId, onDetail, onOpenTool }: {
  messages: UnifiedMessage[]; running: boolean; waiting: boolean; detailId: string | null; onDetail: (id: string | null) => void; onOpenTool: OpenTool;
}) {
  const observations = new Map(messages.flatMap(message => message.payload.case === "observation" ? [[message.payload.value.toolCallId, message] as const] : []));
  const actionIds = new Set(messages.flatMap(message => message.payload.case === "action" ? [message.payload.value.toolCallId] : []));
  const questions = new Map(messages.flatMap(message => message.payload.case === "interaction" ? [[message.payload.value.toolCallId, message] as const] : []));
  const questionActions = new Map(messages.flatMap(message => message.payload.case === "action" && questions.has(message.payload.value.toolCallId) ? [[message.payload.value.toolCallId, message] as const] : []));
  const shownQuestions = new Set<string>();
  const entries: (UnifiedMessage | UnifiedMessage[])[] = [];
  for (const message of messages) {
    if (message.payload.case === "permission") continue;
    const toolCallId = message.payload.case === "action" || message.payload.case === "observation" || message.payload.case === "interaction" ? message.payload.value.toolCallId : undefined;
    const question = toolCallId ? questions.get(toolCallId) : undefined;
    if (question) {
      if (!shownQuestions.has(toolCallId!)) { entries.push(question); shownQuestions.add(toolCallId!); }
      continue;
    }
    if (message.payload.case === "observation" && actionIds.has(message.payload.value.toolCallId)) continue;
    if (message.payload.case === "action" || message.payload.case === "observation") {
      const last = entries.at(-1);
      if (Array.isArray(last)) last.push(message); else entries.push([message]);
    } else entries.push(message);
  }
  const thinking = running && !waiting;
  return <>{entries.map((entry, index) => Array.isArray(entry)
    ? <ToolGroup key={entry[0].id} tools={entry} observations={observations} thinking={thinking && index === entries.length - 1} detailId={detailId} onDetail={onDetail} onOpenTool={onOpenTool} />
    : entry.payload.case === "interaction"
      ? <WorkQuestionRecord key={entry.payload.value.interactionId} message={entry} action={questionActions.get(entry.payload.value.toolCallId)} output={observations.get(entry.payload.value.toolCallId)} open={detailId === entry.id} onToggle={() => onDetail(detailId === entry.id ? null : entry.id)} />
      : <ProcessMessage key={entry.id} message={entry} detailId={detailId} onDetail={onDetail} />)}
    {thinking && !Array.isArray(entries.at(-1)) && <div className="wm-thinking" role="status">Thinking…</div>}
  </>;
}

function ToolGroup({ tools, observations, thinking, detailId, onDetail, onOpenTool }: {
  tools: UnifiedMessage[]; observations: Map<string, UnifiedMessage>; thinking: boolean; detailId: string | null; onDetail: (id: string | null) => void; onOpenTool: OpenTool;
}) {
  const [open, setOpen] = useState(false);
  const contentId = useId();
  return <div className="wm-tool-group">
    <Button variant="ghost" className="wm-tool-summary" aria-expanded={open} aria-controls={contentId} onPress={() => setOpen(!open)}><span className={thinking ? "wm-thinking" : undefined}>{thinking ? "Thinking…" : toolActivity(tools)}</span><ChevronDown size={14} aria-hidden="true" /></Button>
    {open && <div id={contentId}>{tools.map(message => {
      const action = message.payload.case === "action" ? message.payload.value : undefined;
      const output = action ? observations.get(action.toolCallId) : message;
      const name = action?.toolName ?? "Tool output";
      const label = action?.summary ? `${name}: ${action.summary}` : name;
      const Icon = name === "Read" ? FileSearch : name === "Bash" ? Terminal : /Search|Grep|Glob|Fetch/.test(name) ? Search : /^(Edit|Write)$/.test(name) ? FilePenLine : Wrench;
      const preview = action && /^(Read|Write|Edit)$/.test(name);
      const detail = output?.detailFormat ? output : message.detailFormat ? message : undefined;
      const isError = output?.payload.case === "observation" && output.payload.value.isError;
      return <div key={message.id} id={`wm-message-${message.id}`} data-message-id={message.id}>
        <Button variant="ghost" className="wm-activity" aria-expanded={preview ? undefined : detailId === message.id} isDisabled={!preview && !detail} onPress={event => {
          if (preview && event.target instanceof HTMLElement) onOpenTool({ workId: message.agentWorkId, toolCallId: action.toolCallId, title: label }, event.target);
          else if (detail) onDetail(detailId === message.id ? null : message.id);
        }}><Icon size={17} strokeWidth={1.6} /><span title={label}>{label}</span>{isError && <span className="wm-tool-failed">Failed</span>}</Button>
        {!preview && detail && detailId === message.id && <>{action ? <ToolReadingContent tool={{ workId: message.agentWorkId, toolCallId: action.toolCallId, title: label }} /> : <ToolMessageDetail workId={message.agentWorkId} messageId={detail.id} />}</>}
      </div>;
    })}</div>}
  </div>;
}

function ProcessMessage({ message, detailId, onDetail }: { message: UnifiedMessage; detailId: string | null; onDetail: (id: string | null) => void }) {
  const payload = message.payload;
  let text = "";
  if (payload.case === "message") text = messageText(message);
  else if (payload.case === "error") text = payload.value.message || payload.value.code;
  if (!text) return null;
  // The Harness's own note, such as an interruption marker, reads as it was
  // written and as part of the work, never as a person's input.
  if (message.kind === UnifiedMessageKind.HARNESS_NOTE) return <p id={`wm-message-${message.id}`} data-message-id={message.id} className="wm-note">{text}</p>;
  return <div id={`wm-message-${message.id}`} data-message-id={message.id} className={payload.case === "error" ? "wm-error" : "wm-assistant"}>
    <Markdown>{text}</Markdown>
    {payload.case === "error" && message.detailFormat !== MessageDetailFormat.UNSPECIFIED && <><Button variant="ghost" size="sm" aria-expanded={detailId === message.id} onPress={() => onDetail(detailId === message.id ? null : message.id)}>Error details</Button>{detailId === message.id && <ToolMessageDetail workId={message.agentWorkId} messageId={message.id} />}</>}
  </div>;
}

function Result({ message }: { message: UnifiedMessage }) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  if (message.payload.case !== "result") return null;
  const text = message.payload.value.text;
  const time = message.timestamp ? timestampDate(message.timestamp) : undefined;
  return <div className="wm-result" id={`wm-message-${message.id}`} data-message-id={message.id}>
    <Markdown>{text}</Markdown>
    <div className="wm-result-actions"><Button size="sm" variant="ghost" isIconOnly aria-label={copied ? "Copied" : "Copy response"} onPress={async () => {
      try { await navigator.clipboard.writeText(text); setCopied(true); setFailed(false); } catch { setFailed(true); }
    }}>{copied ? <Check size={17} /> : <Copy size={17} />}</Button>{time && <time dateTime={time.toISOString()} title={`Received ${time.toLocaleString("en-US")}`}>{time.toLocaleString("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false })}</time>}{failed && <span role="alert">Could not copy. Select the text to copy it.</span>}</div>
  </div>;
}

function messageText(message: UnifiedMessage) {
  return message.payload.case === "message" ? message.payload.value.content.filter(block => block.type === ContentBlockType.TEXT).map(block => block.text).filter(Boolean).join("\n\n") : "";
}

function toolActivity(tools: UnifiedMessage[]) {
  const counts = new Map<string, number>();
  for (const tool of tools) { const name = tool.payload.case === "action" ? tool.payload.value.toolName : "tool"; counts.set(name, (counts.get(name) ?? 0) + 1); }
  if (counts.size > 1) return `Used tools · ${tools.length} calls`;
  return [...counts].map(([name, count], index) => {
    const text = name === "Read" ? `Read ${count === 1 ? "a file" : `${count} files`}`
      : /^(Edit|Write)$/.test(name) ? `${name === "Edit" ? "Edited" : "Created"} ${count === 1 ? "a file" : `${count} files`}`
      : name === "Bash" ? `Ran ${count === 1 ? "a command" : `${count} commands`}`
      : /^(Grep|Glob)$/.test(name) ? "Searched project files" : /^(WebSearch|WebFetch)$/.test(name) ? "Searched the web" : `Used ${name}${count > 1 ? ` ${count} times` : ""}`;
    return index ? text[0].toLowerCase() + text.slice(1) : text;
  }).join(", ");
}
