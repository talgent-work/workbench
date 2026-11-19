import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { expect, it, vi } from "vitest";
import { ConnectorList, ConnectorPage } from "../components/marketplace/connector-pages";
import { PluginList } from "../components/marketplace/plugin-list";

const userId = "01a11fb2-e6c2-701f-80b7-38c54aa2eae9";

const entry = (connectorId: string, name: string, state: string, publisher: { kind: string; own: boolean; displayName: string }) => ({
  connectorId, name, state, publisher, description: `${name} tools`, category: "Sales", required: false, status: "not_installed", installationVersion: 0,
  provider: "", providerConnection: "", offered: true, transport: "http", iconDigest: "", connectorVersion: 1, publishedAtUnixMs: state === "private" ? 0n : 1_700_000_000_000n,
});
const catalog = [
  entry("01900000-0000-7000-8000-000000000701", "github", "published", { kind: "platform", own: false, displayName: "" }),
  { ...entry("01900000-0000-7000-8000-000000000702", "acme-crm", "published", { kind: "person", own: false, displayName: "Alex Chen" }), status: "enabled", installationVersion: 1 },
  entry("01900000-0000-7000-8000-000000000703", "chrome-devtools", "private", { kind: "person", own: true, displayName: "Me" }),
  { ...entry("01900000-0000-7000-8000-000000000704", "docs", "published", { kind: "person", own: true, displayName: "Me" }), status: "disabled", installationVersion: 1 },
  { ...entry("01900000-0000-7000-8000-000000000705", "talgent", "published", { kind: "platform", own: false, displayName: "" }), required: true },
];
const plugin = (slug: string) => ({ slug, version: "1.0.0", description: `${slug} skills`, nativeEntry: undefined, parameters: {}, skillCount: 1, agentCount: 0, mcpServerCount: 0 });

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: userId } } }) }));
vi.mock("../lib/notifications", () => ({ notify: vi.fn() }));
vi.mock("../lib/standalone-work-service", () => ({ standaloneWorkStartSource: { key: ["standalone"], loadOptions: async () => ({ plugins: [{ name: "core", required: true }] }) } }));
vi.mock("../lib/client", () => ({
  marketplaceClient: {
    listProviders: async () => ({ providers: [] }),
    listConnectors: async () => ({ connectors: catalog }),
    getConnector: async (request: { connectorId: string }) => ({ connector: catalog.find(item => item.connectorId === request.connectorId), plugins: [] }),
    listPlugins: async () => ({ plugins: ["core", "review", "release", "design"].map(plugin), nextCursor: "" }),
    listInstalledPlugins: async () => ({ plugins: [{ slug: "review", enabled: true, configuration: {}, redactedParameters: [] }, { slug: "release", enabled: false, configuration: {}, redactedParameters: [] }], nextCursor: "" }),
  },
}));

function Where() { return <p data-testid="location">{useLocation().pathname}</p>; }

function renderAt(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}><MemoryRouter initialEntries={[path]}>{children}<Where /></MemoryRouter></QueryClientProvider>;
  return render(<Routes>
    <Route path="/marketplace/plugins" element={<PluginList />} />
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

// A row says whether its item is installed and leaves turning it off or
// uninstalling it to its page; only an item not yet installed offers Install.
it("shows each installed Connector's status on its row and keeps the controls on its page", async () => {
  const user = userEvent.setup();
  const { unmount } = renderAt("/marketplace/connectors");
  const installed = await rowOf("acme-crm");
  expect(within(installed).getByText("Installed")).toBeInTheDocument();
  expect(within(installed).queryByRole("button")).toBeNull();
  const off = await rowOf("docs");
  expect(within(off).getByText("Turned off")).toBeInTheDocument();
  expect(within(off).getByText("docs tools")).toBeInTheDocument();
  expect(within(await rowOf("Talgent")).getByLabelText("Always on")).toBeInTheDocument();
  expect(within(await rowOf("chrome-devtools")).getByRole("button", { name: "Install chrome-devtools" })).toBeInTheDocument();
  unmount();

  renderAt("/marketplace/connectors/01900000-0000-7000-8000-000000000704");
  expect(await screen.findByRole("switch", { name: "Use docs in new Works" })).not.toBeChecked();
  await user.click(screen.getByRole("button", { name: "More actions for docs" }));
  expect(await screen.findByRole("menuitem", { name: "Uninstall…" })).toBeInTheDocument();
}, 3000);

it("shows each installed Plugin's status on its row", async () => {
  renderAt("/marketplace/plugins");
  expect(within(await rowOf("Review")).getByText("Installed")).toBeInTheDocument();
  const off = await rowOf("Release");
  expect(within(off).getByText("Turned off")).toBeInTheDocument();
  expect(within(off).getByText("release skills")).toBeInTheDocument();
  expect(within(await rowOf("Core")).getByLabelText("Always on")).toBeInTheDocument();
  expect(within(await rowOf("Design")).getByRole("button", { name: "Install Design" })).toBeInTheDocument();
}, 3000);
