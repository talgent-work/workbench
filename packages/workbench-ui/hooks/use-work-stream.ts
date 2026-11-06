import { useEffect, useRef } from "react";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { UnifiedMessageKind, type UnifiedMessage } from "@talgent/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import type { StreamDiagnosticFrame, WorkStateFrame, WatchStreamResponse } from "@talgent/workbench-contracts/streamgateway/public/v1/streamgateway_pb";
import { useWorkStreamSource } from "../lib/work-stream-source";

interface WorkStreamOptions {
  workId?: string;
  kinds?: UnifiedMessageKind[];
  includeUnattributedErrors?: boolean;
  onMessage: (message: UnifiedMessage, replay: boolean) => void;
  onWorkState?: (state: WorkStateFrame) => void;
  /** A live notice that is not part of the message stream and is never replayed. */
  onDiagnostic?: (diagnostic: StreamDiagnosticFrame) => void;
  onReconnect?: () => void;
  /** The reader's access ended; the stream stops and does not reconnect. */
  onAccessWithdrawn?: () => void;
}

// Use the slim product contract directly. Connection diagnostics are connection
// state, never fabricated Harness errors or transcript messages.
export function useWorkStream(options: WorkStreamOptions) {
  const callbacks = useRef(options);
  useEffect(() => { callbacks.current = options; });
  const { workId, kinds, includeUnattributedErrors = false } = options;
  const source = useWorkStreamSource();
  const kindsKey = kinds?.join(",") ?? "";

  useEffect(() => {
    if (!workId) return;
    const abort = new AbortController();
    const filters = kindsKey ? kindsKey.split(",").map(Number) as UnifiedMessageKind[] : [];
    let cursor: { timestamp?: Timestamp; id: string } = { id: "" };
    let attempt = 0;
    let reconnecting = false;
    void (async () => {
      while (!abort.signal.aborted) {
        if (reconnecting) callbacks.current.onReconnect?.();
        try {
          const events: AsyncIterable<WatchStreamResponse> = source.watchStream({
            agentWorkId: workId, agent: "", kinds: filters, includeUnattributedErrors,
            afterTimestamp: cursor.timestamp, afterId: cursor.id,
          }, { signal: abort.signal });
          for await (const event of events) {
            if (abort.signal.aborted) break;
            attempt = 0;
            if (event.payload.case === "message") {
              const message: UnifiedMessage = event.payload.value;
              const unknownError = includeUnattributedErrors && message.agent === undefined && message.kind === UnifiedMessageKind.ERROR;
              if (message.agentWorkId !== workId || (message.agent !== "" && !unknownError) || (filters.length && !filters.includes(message.kind))) continue;
              callbacks.current.onMessage(message, event.replay);
              if (message.timestamp && (!cursor.timestamp || compareMessageCursor(message.timestamp, message.id, cursor.timestamp, cursor.id) > 0)) {
                cursor = { timestamp: message.timestamp, id: message.id };
              }
            } else if (event.payload.case === "workState" && event.payload.value.agentWorkId === workId) {
              callbacks.current.onWorkState?.(event.payload.value);
            } else if (event.payload.case === "diagnostic" && event.payload.value.agentWorkId === workId) {
              callbacks.current.onDiagnostic?.(event.payload.value);
            } else if (event.payload.case === "accessWithdrawn" && event.payload.value.agentWorkId === workId) {
              // Withdrawal is not a connection error: never reconnect.
              callbacks.current.onAccessWithdrawn?.();
              return;
            }
          }
        } catch {
          // Reconnect below from the last received cursor without a UI warning.
        }
        if (abort.signal.aborted) break;
        reconnecting = true;
        await reconnectDelay(Math.min(5000, 250 * 2 ** attempt++), abort.signal);
      }
    })();
    return () => abort.abort();
  }, [workId, kindsKey, includeUnattributedErrors, source]);

}

export function compareMessageCursor(a: Timestamp, aID: string, b: Timestamp, bID: string) {
  if (a.seconds !== b.seconds) return a.seconds < b.seconds ? -1 : 1;
  if (a.nanos !== b.nanos) return a.nanos < b.nanos ? -1 : 1;
  return aID < bID ? -1 : aID > bID ? 1 : 0;
}

function reconnectDelay(ms: number, signal: AbortSignal) {
  return new Promise<void>(resolve => {
    const done = () => { window.clearTimeout(timer); signal.removeEventListener("abort", done); resolve(); };
    const timer = window.setTimeout(done, ms);
    signal.addEventListener("abort", done, { once: true });
    if (signal.aborted) done();
  });
}
