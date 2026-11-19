import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { expect, it, vi } from "vitest";
import { ConnectorList, ConnectorPage } from "../components/marketplace/connector-pages";

const userId = "01a11fb2-e6c2-701f-80b7-38c54aa2eae9";

const entry = (connectorId: string, name: string, state: string, publisher: { kind: string; own: boolean; displayName: string }) => ({
  connectorId, name, state, publisher, description: `${name} tools`, category: "Sales", required: false, status: "not_installed", installationVersion: 0,
  provider: "", providerConnection: "", offered: true, transport: "http", iconDigest: "", connectorVersion: 1, publishedAtUnixMs: state === "private" ? 0n : 1_700_000_000_000n,
});
const catalog = [
  entry("01900000-0000-7000-8000-000000000701", "github", "published", { kind: "platform", own: false, displayName: "" }),
  entry("01900000-0000-7000-8000-000000000702", "acme-crm", "published", { kind: "person", own: false, displayName: "Alex Chen" }),
  entry("01900000-0000-7000-8000-000000000703", "chrome-devtools", "private", { kind: "person", own: true, displayName: "Me" }),
  entry("01900000-0000-7000-8000-000000000704", "docs", "published", { kind: "person", own: true, displayName: "Me" }),
];

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: userId } } }) }));
vi.mock("../lib/notifications", () => ({ notify: vi.fn() }));
vi.mock("../lib/standalone-work-service", () => ({ standaloneWorkStartSource: { key: ["standalone"], loadOptions: async () => ({ plugins: [] }) } }));
vi.mock("../lib/client", () => ({
  marketplaceClient: {
    listProviders: async () => ({ providers: [] }),
    listConnectors: async () => ({ connectors: catalog }),
    getConnector: async (request: { connectorId: string }) => ({ connector: catalog.find(item => item.connectorId === request.connectorId), plugins: [] }),
    listPlugins: async () => ({ plugins: [], nextCursor: "" }),
    listInstalledPlugins: async () => ({ plugins: [], nextCursor: "" }),
  },
}));

function Where() { return <p data-testid="location">{useLocation().pathname}</p>; }

function renderAt(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}><MemoryRouter initialEntries={[path]}>{children}<Where /></MemoryRouter></QueryClientProvider>;
  return render(<Routes>
    <Route path="/marketplace/connectors" element={<ConnectorList />} />
    <Route path="/marketplace/connectors/:connectorId" element={<ConnectorPage connectorId={path.split("/").at(-1)!} />} />
  </Routes>, { wrapper });
}

const rowOf = async (name: string) => (await screen.findByRole("link", { name: new RegExp(`^${name}`) })).closest("li")!;

it("names each Connector's publisher in the list and marks the person's own Private one", async () => {
  renderAt("/marketplace/connectors");
  expect(within(await rowOf("GitHub")).getByText("Talgent")).toBeInTheDocument();
  expect(within(await rowOf("acme-crm")).getByText("Alex Chen")).toBeInTheDocument();
  const own = await rowOf("chrome-devtools");
  expect(within(own).getByText("Private")).toBeInTheDocument();
  expect(within(own).getByText("Only you see it")).toBeInTheDocument();
  expect(within(await rowOf("docs")).getByText("Published by you")).toBeInTheDocument();
}, 3000);

it("links a person's own Connector to its Workshop page, and only their own", async () => {
  const user = userEvent.setup();
  const { unmount } = renderAt("/marketplace/connectors/01900000-0000-7000-8000-000000000702");
  expect(await screen.findByText("Published by Alex Chen")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Manage in workshop" })).toBeNull();
  unmount();

  renderAt("/marketplace/connectors/01900000-0000-7000-8000-000000000703");
  await user.click(await screen.findByRole("button", { name: "Manage in workshop" }));
  expect(screen.getByTestId("location")).toHaveTextContent("/workshop/connectors/01900000-0000-7000-8000-000000000703");
}, 3000);
