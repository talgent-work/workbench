import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { useAlwaysEnabledPlugins } from "../lib/marketplace-catalog";

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: "user-1" } } }) }));
// A deployment whose Agents have no direct API access leaves the core API
// plugin out and declares a Built-in Plugin of its own.
vi.mock("../lib/client", () => ({
  workClient: {
    getWorkStartOptions: async () => ({ options: { models: [], connectors: [], plugins: [
      { name: "talgent", required: true },
      { name: "example-plugin", required: true },
      { name: "chosen-plugin", required: false },
    ] } }),
  },
}));

const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;

it("treats exactly the Plugins the server requires of a new Work as always enabled", async () => {
  const { result } = renderHook(() => useAlwaysEnabledPlugins(), { wrapper });
  expect(result.current.isPending).toBe(true);

  await waitFor(() => expect(result.current.isPending).toBe(false));

  expect(result.current.isAlwaysEnabled("talgent")).toBe(true);
  expect(result.current.isAlwaysEnabled("example-plugin")).toBe(true);
  expect(result.current.isAlwaysEnabled("talgent-api")).toBe(false);
  expect(result.current.isAlwaysEnabled("chosen-plugin")).toBe(false);
});
