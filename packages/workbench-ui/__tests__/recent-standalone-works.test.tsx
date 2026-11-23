import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { useRecentStandaloneWorks } from "../lib/standalone-work-service";

type ListWorksRequest = { limit: number; states: string[] };
const listWorks = vi.fn<(request: ListWorksRequest) => Promise<unknown>>(async () => ({ works: [{ workId: "work-1", title: "Open work", state: "stopped", stateReason: "", waiting: false }] }));
vi.mock("../lib/client", () => ({ workClient: { listWorks: (request: ListWorksRequest) => listWorks(request) } }));
vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: "user-1" } } }) }));

const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;

it("asks for the latest Works in every state but closed, so Recent stays full without Closed Works", async () => {
  const { result } = renderHook(() => useRecentStandaloneWorks(20), { wrapper });

  await waitFor(() => expect(result.current.data?.map(work => work.id)).toEqual(["work-1"]));
  const request = listWorks.mock.calls[0][0];
  expect(request.limit).toBe(20);
  expect(request.states).not.toContain("closed");
  expect(request.states).toEqual(expect.arrayContaining(["starting", "working", "stopping", "stopped", "failed", "closing"]));
});
