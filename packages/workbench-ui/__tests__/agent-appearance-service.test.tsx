import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { showsWorkAgent, useChangeMyWorkAgentAppearance } from "../lib/agent-appearance-service";

const userId = "01a11fb2-e6c2-701f-80b7-38c54aa2eae9";
vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: userId } } }) }));
vi.mock("../lib/client", () => ({
  agentWorkClient: {
    ensureMyWorkAgent: async () => ({ agentId: "agent-octo" }),
    changeWorkAgentAppearance: async (request: { name: string; icon: unknown }) => ({ appearance: { name: request.name, icon: request.icon } }),
  },
}));

// Every place the Work Agent appears reads its current appearance, whatever
// page or entry declared the query.
it("refreshes every query that shows the Work Agent once its appearance is saved", async () => {
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  await queryClient.prefetchQuery({ queryKey: ["thread", "t-1"], queryFn: () => ({ title: "a thread" }), meta: showsWorkAgent });
  await queryClient.prefetchQuery({ queryKey: ["unrelated"], queryFn: () => ({ value: 1 }) });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  const { result } = renderHook(() => useChangeMyWorkAgentAppearance(), { wrapper });

  await act(async () => { await result.current.mutateAsync({ agentId: "", name: "Octo", icon: { glyph: "bot", color: "green" } }); });

  await waitFor(() => expect(queryClient.getQueryState(["thread", "t-1"])?.isInvalidated).toBe(true));
  expect(queryClient.getQueryState(["unrelated"])?.isInvalidated).toBe(false);
});
