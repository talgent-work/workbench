import { useCallback, useEffect, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { UnifiedMessageKind, type UnifiedMessage } from "@talgent/contracts/dispatcher/public/v1/unified_message_pb";
import type { StreamDiagnosticFrame, WorkStateFrame, WatchStreamResponse } from "@talgent/contracts/streamgateway/public/v1/streamgateway_pb";
import { useWorkStreamSource } from "../lib/work-stream-source";

interface WorkStreamOptions {
  workId?: string;
  kinds?: UnifiedMessageKind[];
  includeUnattributedErrors?: boolean;
  onMessage: (message: UnifiedMessage) => void;
  onWorkState?: (state: WorkStateFrame) => void;
  /** A live notice that is not part of the message stream and is never stored. */
  onDiagnostic?: (diagnostic: StreamDiagnosticFrame) => void;
  /** A reconnected stream is listening again; what it missed is in history. */
  onReconnect?: () => void;
  /** The reader's access ended; the stream stops and does not reconnect. */
  onAccessWithdrawn?: () => void;
}

/**
 * live: frames are arriving. connecting: the stream is opening or waiting to
 * retry. paused: the page is hidden. stopped: the stream ended for good, as
 * when access was refused. disconnected: retries ran out; `reconnect` resumes.
 */
export type WorkStreamStatus = "connecting" | "live" | "paused" | "stopped" | "disconnected";

// The stream sends a frame at least every 30 seconds, so a stream silent for
// longer than two of those is taken as dead.
const silenceLimitMs = 75_000;
const backoffBaseMs = 1_000;
const backoffCapMs = 30_000;
// Consecutive failed attempts before the reader is asked to reconnect.
const maxAttempts = 8;
// A stream open this long counts as healthy, so its next failure starts the
// backoff afresh.
const healthyAfterMs = 60_000;
// A page hidden this long closes its stream until it is shown again.
const hiddenGraceMs = 60_000;

// These answers do not change by asking again.
const terminalCodes = new Set([Code.PermissionDenied, Code.NotFound, Code.InvalidArgument, Code.FailedPrecondition, Code.Unimplemented]);

/** A Work's live frames. History is read apart and merged by message ID. */
export function useWorkStream(options: WorkStreamOptions) {
  const callbacks = useRef(options);
  useEffect(() => { callbacks.current = options; });
  const { workId, kinds, includeUnattributedErrors = false } = options;
  const source = useWorkStreamSource();
  const kindsKey = kinds?.join(",") ?? "";
  const [status, setStatus] = useState<WorkStreamStatus>("connecting");
  const [generation, setGeneration] = useState(0);
  const reconnect = useCallback(() => setGeneration(value => value + 1), []);

  useEffect(() => {
    if (!workId) return;
    const lifetime = new AbortController();
    const filters = kindsKey ? kindsKey.split(",").map(Number) as UnifiedMessageKind[] : [];
    let wake: (() => void) | undefined;
    let current: AbortController | undefined;
    let hiddenTimer: number | undefined;
    let paused = false;

    const onVisibility = () => {
      window.clearTimeout(hiddenTimer);
      if (document.hidden) {
        hiddenTimer = window.setTimeout(() => { paused = true; current?.abort(); }, hiddenGraceMs);
      } else if (paused) {
        paused = false;
        wake?.();
      }
    };
    const onOnline = () => wake?.();
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("online", onOnline);

    void (async () => {
      let attempt = 0;
      // After a manual reconnect, the first stream already follows a gap.
      let opened = generation > 0;
      setStatus("connecting");
      while (!lifetime.signal.aborted) {
        if (paused) {
          setStatus("paused");
          await until(lifetime.signal, resume => { wake = resume; });
          continue;
        }
        const call = new AbortController();
        current = call;
        const stop = () => call.abort();
        lifetime.signal.addEventListener("abort", stop, { once: true });
        let silence = window.setTimeout(stop, silenceLimitMs);
        let listeningSince: number | undefined;
        let outcome: "retry" | "stop" = "retry";
        try {
          const events: AsyncIterable<WatchStreamResponse> = source.watchStream({
            agentWorkId: workId, agent: "", kinds: filters, includeUnattributedErrors,
          }, { signal: call.signal });
          for await (const event of events) {
            if (call.signal.aborted) break;
            window.clearTimeout(silence);
            silence = window.setTimeout(stop, silenceLimitMs);
            if (listeningSince === undefined) {
              // The first frame says the stream is listening; history read
              // from now on covers whatever the gap held.
              listeningSince = Date.now();
              setStatus("live");
              if (opened) callbacks.current.onReconnect?.();
              opened = true;
            }
            if (event.payload.case === "message") {
              const message: UnifiedMessage = event.payload.value;
              const unknownError = includeUnattributedErrors && message.agent === undefined && message.kind === UnifiedMessageKind.ERROR;
              if (message.agentWorkId !== workId || (message.agent !== "" && !unknownError) || (filters.length && !filters.includes(message.kind))) continue;
              callbacks.current.onMessage(message);
            } else if (event.payload.case === "workState" && event.payload.value.agentWorkId === workId) {
              callbacks.current.onWorkState?.(event.payload.value);
            } else if (event.payload.case === "diagnostic" && event.payload.value.agentWorkId === workId) {
              callbacks.current.onDiagnostic?.(event.payload.value);
            } else if (event.payload.case === "accessWithdrawn" && event.payload.value.agentWorkId === workId) {
              // Withdrawal is not a connection error: never reconnect.
              callbacks.current.onAccessWithdrawn?.();
              outcome = "stop";
              break;
            }
          }
        } catch (error) {
          if (error instanceof ConnectError && terminalCodes.has(error.code) && !call.signal.aborted) outcome = "stop";
        } finally {
          window.clearTimeout(silence);
          lifetime.signal.removeEventListener("abort", stop);
        }
        if (lifetime.signal.aborted) return;
        if (outcome === "stop") {
          setStatus("stopped");
          return;
        }
        if (paused) continue;
        if (listeningSince !== undefined && Date.now() - listeningSince >= healthyAfterMs) attempt = 0;
        if (++attempt > maxAttempts) {
          setStatus("disconnected");
          return;
        }
        setStatus("connecting");
        // Full jitter: readers that lost the same server do not return together.
        const ceiling = Math.min(backoffCapMs, backoffBaseMs * 2 ** (attempt - 1));
        await until(lifetime.signal, resume => { wake = resume; }, Math.random() * ceiling);
      }
    })();
    return () => {
      lifetime.abort();
      window.clearTimeout(hiddenTimer);
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("online", onOnline);
    };
  }, [workId, kindsKey, includeUnattributedErrors, source, generation]);

  return { status, reconnect };
}

/** Waits for wake, the optional delay, or the end of signal, whichever comes first. */
function until(signal: AbortSignal, register: (wake: () => void) => void, ms?: number) {
  return new Promise<void>(resolve => {
    const timer = ms === undefined ? undefined : window.setTimeout(done, ms);
    function done() {
      window.clearTimeout(timer);
      signal.removeEventListener("abort", done);
      register(() => {});
      resolve();
    }
    register(done);
    signal.addEventListener("abort", done, { once: true });
    if (signal.aborted) done();
  });
}

export function compareMessageCursor(a: Timestamp, aID: string, b: Timestamp, bID: string) {
  if (a.seconds !== b.seconds) return a.seconds < b.seconds ? -1 : 1;
  if (a.nanos !== b.nanos) return a.nanos < b.nanos ? -1 : 1;
  return aID < bID ? -1 : aID > bID ? 1 : 0;
}
