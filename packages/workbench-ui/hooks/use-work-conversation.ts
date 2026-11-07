import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import type { MessageHistoryCursor, WorkStateFrame } from "@talgent/contracts/streamgateway/public/v1/streamgateway_pb";
import { ContentBlockType, UnifiedMessageKind, type UnifiedMessage } from "@talgent/contracts/dispatcher/public/v1/unified_message_pb";
import { useWorkStreamSource } from "../lib/work-stream-source";
import { WorkConversation } from "../lib/work-conversation";
import { compareMessageCursor, useWorkStream } from "./use-work-stream";
import { notifyWorkNotice } from "../lib/work-notices";

interface TurnAnchor { id: string; turnId: string; title: string; timestamp: NonNullable<UnifiedMessage["timestamp"]> }
function turnAnchor(message: UnifiedMessage): TurnAnchor[] {
  if (message.kind !== UnifiedMessageKind.USER || message.agent !== "" || !message.turnId || !message.timestamp || message.payload.case !== "message") return [];
  const title = message.payload.value.content.filter(block => block.type === ContentBlockType.TEXT).map(block => block.text.slice(0, 160)).join("\n\n").slice(0, 160) || "Image attachment";
  return [{ id: message.id, turnId: message.turnId, timestamp: message.timestamp, title }];
}
const cursorFor = (anchor: { id: string; timestamp: TurnAnchor["timestamp"] }): MessageHistoryCursor => ({ $typeName: "streamgateway.public.v1.MessageHistoryCursor", id: anchor.id, timestamp: anchor.timestamp });

export function useWorkConversation(workId: string | undefined, onWorkState?: (state: WorkStateFrame) => void, onReconnect?: () => void, onActivity?: () => void, onAccessWithdrawn?: () => void) {
  const streamClient = useWorkStreamSource();
  const [navigation, setNavigation] = useState<{ workId?: string; generation: number; before?: MessageHistoryCursor; from?: MessageHistoryCursor }>({ workId, generation: 0 });
  const generation = navigation.workId === workId ? navigation.generation : 0;
  const before = navigation.workId === workId ? navigation.before : undefined;
  const from = navigation.workId === workId ? navigation.from : undefined;
  const browsingHistory = Boolean(before || from);
  const [liveAnchors, setLiveAnchors] = useState<{ workId?: string; users: TurnAnchor[] }>({ workId, users: [] });
  // Index only user messages, independently of the bounded transcript window.
  // Retain titles/cursors, never image bodies or the original query response.
  const index = useInfiniteQuery({
    queryKey: ["work-turn-index", workId], enabled: Boolean(workId),
    initialPageParam: undefined as MessageHistoryCursor | undefined,
    queryFn: async ({ pageParam, signal }) => {
      const page = await streamClient.listMessagesBySession({ agentWorkId: workId!, agent: "", kinds: [UnifiedMessageKind.USER], limit: 100, before: pageParam }, { signal });
      return { users: page.messages.flatMap(turnAnchor), next: page.hasMore ? page.nextBefore : undefined };
    },
    getNextPageParam: page => page.next,
    maxPages: 5, retry: false, gcTime: 0, staleTime: Infinity, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  const liveTail = useMemo(() => new WorkConversation(workId ?? "", 100, 2 * 1024 * 1024), [workId]);
  // Returning to latest deliberately replaces the retained history window.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const conversation = useMemo(() => new WorkConversation(workId ?? ""), [workId, generation]);
  // Replay bursts update the bounded window immediately, but notify React at
  // most once per frame instead of rendering the transcript for each item.
  const subscribe = useCallback((listener: () => void) => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = conversation.subscribe(() => {
      timer ??= setTimeout(() => { timer = undefined; listener(); }, 16);
    });
    return () => { clearTimeout(timer); unsubscribe(); };
  }, [conversation]);
  const snapshot = useSyncExternalStore(subscribe, conversation.getSnapshot);
  const [arrival, setArrival] = useState<WorkConversation | null>(null);
  const history = useQuery({
    queryKey: ["work-conversation", workId, generation, before?.timestamp?.seconds.toString(), before?.timestamp?.nanos, before?.id, from?.timestamp?.seconds.toString(), from?.timestamp?.nanos, from?.id],
    enabled: Boolean(workId),
    queryFn: async ({ signal }) => {
      const page = await streamClient.listMessagesBySession({ agentWorkId: workId!, agent: "", includeUnattributedErrors: true, limit: 100, before, from }, { signal });
      // Apply the same byte ceiling before retaining the query response too.
      const window = new WorkConversation(workId!, 100);
      window.add(page.messages, from ? "older" : "history");
      const retained = window.getSnapshot();
      return { ...page, messages: retained.turns.flatMap(turn => turn.messages), hasMore: page.hasMore || retained.olderEvicted };
    },
    retry: false, gcTime: 0, staleTime: Infinity, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  useEffect(() => {
    // Live delivery can precede visibility in stored history. Reuse the bounded
    // tail on a window reset rather than losing those messages during that gap.
    if (from) return;
    conversation.add(liveTail.getSnapshot().turns.flatMap(turn => turn.messages), "replay");
  // A forward cursor changes within the same window; seed only on reset.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conversation, liveTail]);
  useEffect(() => {
    if (history.data) conversation.add(history.data.messages, before ? "older" : "history");
  }, [conversation, history.data, before]);
  const navigationTurns = useMemo(() => {
    const users = [...(index.data?.pages.flatMap(page => page.users) ?? []), ...(liveAnchors.workId === workId ? liveAnchors.users : []), ...snapshot.turns.flatMap(turn => turn.messages.flatMap(turnAnchor))];
    users.sort((a, b) => compareMessageCursor(a.timestamp, a.id, b.timestamp, b.id));
    const turns = new Map<string, TurnAnchor>();
    for (const user of users) if (!turns.has(user.turnId)) turns.set(user.turnId, user);
    return [...turns.values()];
  }, [index.data, liveAnchors, workId, snapshot.turns]);
  useWorkStream({
    workId, includeUnattributedErrors: true, onWorkState, onAccessWithdrawn,
    onDiagnostic: diagnostic => { if (workId) notifyWorkNotice(workId, diagnostic); },
    onMessage: (message, replay) => {
      if (!replay && [UnifiedMessageKind.USER, UnifiedMessageKind.RESULT, UnifiedMessageKind.PERMISSION, UnifiedMessageKind.INTERACTION, UnifiedMessageKind.ERROR].includes(message.kind)) onActivity?.();
      const anchors = turnAnchor(message);
      if (anchors.length) setLiveAnchors(current => ({ workId, users: [...(current.workId === workId ? current.users.filter(user => user.id !== message.id) : []), ...anchors].slice(-100) }));
      liveTail.add([message], replay ? "replay" : "live");
      // While reading older pages, keep the reading window intact. Returning to
      // latest combines authoritative history with the bounded recent tail.
      if (browsingHistory && !conversation.has(message.id)) {
        if (!replay && message.kind === UnifiedMessageKind.USER) setNavigation({ workId, generation });
        else { setArrival(conversation); return; }
      }
      conversation.add([message], replay ? "replay" : "live");
    },
    onReconnect: () => { if (!browsingHistory) void history.refetch(); void index.refetch(); onReconnect?.(); },
  });
  const hasMore = Boolean(from || history.data?.hasMore || snapshot.olderEvicted);
  return {
    ...snapshot,
    loading: history.isFetching, error: history.isError, hasMore,
    navigationTurns, indexLoading: index.isFetching, indexError: index.isError, hasOlderTurns: Boolean(index.hasNextPage),
    loadOlderTurns: () => { if (!index.isFetching) void index.fetchNextPage(); },
    retryIndex: () => { void index.refetch(); },
    browsingHistory, hasNewer: arrival === conversation,
    retry: () => { void history.refetch(); },
    loadOlder: () => {
      const oldest = conversation.getOldest();
      if (!workId || history.isFetching || !hasMore || !oldest?.timestamp) return;
      setNavigation({ workId, generation, before: { $typeName: "streamgateway.public.v1.MessageHistoryCursor", timestamp: oldest.timestamp, id: oldest.id } });
    },
    hasLaterMessages: browsingHistory && (Boolean(before) || Boolean(history.data?.hasMore)),
    loadNewer: () => {
      const newest = conversation.getNewest();
      if (!workId || history.isFetching || !newest?.timestamp) return;
      setNavigation({ workId, generation, from: cursorFor({ id: newest.id, timestamp: newest.timestamp }) });
    },
    showTurn: (anchor: TurnAnchor) => setNavigation({ workId, generation: generation + 1, from: cursorFor(anchor) }),
    showLatest: () => setNavigation({ workId, generation: generation + 1 }),
  };
}
