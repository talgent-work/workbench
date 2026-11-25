import { act, cleanup, renderHook } from "@testing-library/react";
import { Code, ConnectError } from "@connectrpc/connect";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useWorkStream } from "../hooks/use-work-stream";
import { WorkStreamSourceContext, type WorkStreamSource } from "../lib/work-stream-source";

// Each call to watchStream runs the next scripted stream.
type Script = (signal: AbortSignal) => AsyncIterable<unknown>;
const heartbeat = { payload: { case: "heartbeat", value: {} } };

function sourceOf(scripts: Script[]) {
  const watchStream = vi.fn((_request: unknown, options?: { signal?: AbortSignal }) => {
    const next = scripts.shift() ?? failing(Code.Unavailable);
    return next(options!.signal!);
  });
  return { source: { watchStream } as unknown as WorkStreamSource, watchStream };
}

const failing = (code: Code): Script => () => ({
  [Symbol.asyncIterator]: () => ({ next: () => Promise.reject(new ConnectError("refused", code)) }),
});
// A stream that says it is listening, then stays silent until aborted.
const listening: Script = async function* (signal) {
  yield heartbeat;
  await new Promise(resolve => signal.addEventListener("abort", resolve, { once: true }));
};
// A stream that says it is listening, then ends.
const ending: Script = async function* () { yield heartbeat; };

function render(source: WorkStreamSource, onReconnect = vi.fn()) {
  const wrapper = ({ children }: { children: ReactNode }) => <WorkStreamSourceContext.Provider value={source}>{children}</WorkStreamSourceContext.Provider>;
  return renderHook(() => useWorkStream({ workId: "work", onMessage: vi.fn(), onReconnect }), { wrapper });
}

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { cleanup(); vi.useRealTimers(); });

const settle = () => act(() => vi.advanceTimersByTimeAsync(0));
const pass = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));

// An answer that asking again cannot change ends the stream after one attempt.
it("stops at once when the Work refuses the reader", async () => {
  const { source, watchStream } = sourceOf([failing(Code.PermissionDenied)]);
  const { result } = render(source);
  await settle();
  await pass(120_000);
  expect(watchStream).toHaveBeenCalledTimes(1);
  expect(result.current.status).toBe("stopped");
});

// Retryable failures back off and give up after a bound; the reader can then
// reconnect by hand.
it("gives up after repeated failures and reconnects on request", async () => {
  const { source, watchStream } = sourceOf([]);
  const { result } = render(source);
  for (let i = 0; i < 10; i++) await pass(30_000);
  expect(watchStream).toHaveBeenCalledTimes(9);
  expect(result.current.status).toBe("disconnected");

  await act(async () => { result.current.reconnect(); });
  await settle();
  expect(watchStream).toHaveBeenCalledTimes(10);
});

// A reconnected stream that is listening again asks for the history it missed,
// and only then; the first stream does not.
it("asks for missed history once a reconnected stream is listening", async () => {
  const onReconnect = vi.fn();
  const { source, watchStream } = sourceOf([ending, listening]);
  const { result } = render(source, onReconnect);
  await settle();
  expect(onReconnect).not.toHaveBeenCalled();
  await pass(1_000);
  expect(watchStream).toHaveBeenCalledTimes(2);
  expect(onReconnect).toHaveBeenCalledTimes(1);
  expect(result.current.status).toBe("live");
});

// A stream silent for longer than its heartbeats allow is taken as dead.
it("reconnects a stream that has gone silent", async () => {
  const { source, watchStream } = sourceOf([listening, listening]);
  render(source);
  await settle();
  await pass(74_000);
  expect(watchStream).toHaveBeenCalledTimes(1);
  await pass(2_000);
  await pass(1_000);
  expect(watchStream).toHaveBeenCalledTimes(2);
});
