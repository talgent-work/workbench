import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { ProviderPage, ProviderRows } from "../components/marketplace/provider-pages";
import { useProviderAuthorizationCallback } from "../components/profile/provider-callback";

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: "user" } } }) }));
const notify = vi.fn();
vi.mock("../lib/notifications", () => ({ notify: (...args: unknown[]) => notify(...args) }));

const github = { name: "github", displayName: "GitHub", targets: [{ name: "github-api", hosts: ["api.github.com"] }], targetCredentialsExpire: true, observesAccessScope: true,
  connection: { provider: "github", status: "none", authorizationId: "" } };
const sso = { name: "example-sso", displayName: "Example SSO", targets: [{ name: "wiki", hosts: ["wiki.example.com"] }], targetCredentialsExpire: false, observesAccessScope: false,
  connection: { provider: "example-sso", status: "active", authorizationId: "authorization", providerLogin: "alice" } };
const completed: Array<{ provider: string }> = [];
vi.mock("../lib/client", () => ({
  marketplaceClient: {
    listProviders: async () => ({ providers: [github, sso] }),
    getProviderAccess: async ({ provider }: { provider: string }) => ({ state: provider === "example-sso" ? sso.connection : github.connection, accounts: [], permissions: [], manageUrl: "" }),
    listConnectors: async () => ({ connectors: [] }),
    completeProviderAuthorization: async (request: { provider: string }) => {
      completed.push(request);
      return { authorization: { id: "authorization", providerLogin: "alice" } };
    },
  },
}));

const wrapper = (path: string) => function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter initialEntries={[path]}>{children}</MemoryRouter></QueryClientProvider>;
};

beforeEach(() => {
  completed.length = 0;
  notify.mockClear();
  window.sessionStorage.clear();
});

it("lists every Provider the deployment declares, with its connection", async () => {
  render(<ProviderRows />, { wrapper: wrapper("/profile") });
  const rows = await screen.findAllByRole("listitem");
  expect(rows.map(row => row.querySelector("strong")?.textContent)).toEqual(["GitHub", "Example SSO"]);
  expect(within(rows[1]).getByText("Connected")).toBeInTheDocument();
  expect(within(rows[1]).getByRole("link")).toHaveAttribute("href", expect.stringContaining("example-sso"));
});

it("shows organizations only for a Provider that decides which accounts an authorization reaches", async () => {
  render(<ProviderPage provider="example-sso" />, { wrapper: wrapper("/marketplace/providers/example-sso") });
  expect(await screen.findByRole("heading", { name: "Example SSO" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Disconnect" })).toBeInTheDocument();
  expect(screen.queryByText(/Organizations/)).not.toBeInTheDocument();
});

function Callback() {
  useProviderAuthorizationCallback();
  return null;
}

it("completes the connection for the Provider it began with", async () => {
  window.sessionStorage.setItem("talgent.providerAuthorization.provider", "example-sso");
  window.sessionStorage.setItem("talgent.providerAuthorization.return", "/profile");
  render(<Routes><Route path="*" element={<Callback />} /></Routes>, { wrapper: wrapper("/profile?code=code&state=state") });
  await vi.waitFor(() => expect(completed).toEqual([expect.objectContaining({ provider: "example-sso", code: "code" })]));
  expect(window.sessionStorage.length).toBe(0);
});
