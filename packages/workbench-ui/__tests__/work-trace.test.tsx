import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { useWorkTrace } from "../lib/use-work-trace";
import { isExtensionWorkCaller } from "../lib/work-caller";

type MetricsRequest = { workCaller?: { kind: string; id: string }; agentWorkId: string };
const requests: MetricsRequest[] = [];
const getSessionTraceMetrics = vi.fn(async (request: MetricsRequest) => {
  requests.push(request);
  return { buckets: [], totals: [] };
});
vi.mock("../lib/client", () => ({ traceClient: { getSessionTraceMetrics: (request: MetricsRequest) => getSessionTraceMetrics(request) } }));

beforeEach(() => {
  getSessionTraceMetrics.mockClear();
  requests.length = 0;
});

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

// A Standalone Work is read by its own identity, with no Work Caller.
it("reads a Standalone Work's usage without a Work Caller", async () => {
  renderHook(() => useWorkTrace({ agentWorkId: "work-1" }), { wrapper });
  await waitFor(() => expect(getSessionTraceMetrics).toHaveBeenCalled());
  expect(requests[0]).toMatchObject({ agentWorkId: "work-1", workCaller: undefined });
});

// An Extension Work is read under its Work Caller, whose extension admits the
// reader.
it("reads an Extension Work's usage under its Work Caller", async () => {
  renderHook(() => useWorkTrace({ workCaller: { kind: "team", id: "team-1" }, agentWorkId: "work-2" }), { wrapper });
  await waitFor(() => expect(getSessionTraceMetrics).toHaveBeenCalled());
  expect(requests[0]).toMatchObject({ agentWorkId: "work-2", workCaller: { kind: "team", id: "team-1" } });
});

// A person and an Application start Standalone Works; every other kind is an
// extension's.
it("tells Extension Works by their Work Caller's kind", () => {
  expect(isExtensionWorkCaller({ kind: "team" })).toBe(true);
  expect(isExtensionWorkCaller({ kind: "person" })).toBe(false);
  expect(isExtensionWorkCaller({ kind: "application" })).toBe(false);
  expect(isExtensionWorkCaller({ kind: "" })).toBe(false);
  expect(isExtensionWorkCaller(undefined)).toBe(false);
});
