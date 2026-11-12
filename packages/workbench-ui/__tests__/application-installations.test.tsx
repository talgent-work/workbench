import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { InstallationsSection } from "../pages/applications/installations-section";
import type { Application } from "../lib/application-service";

const applicationId = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e";
const application = { id: applicationId, name: "Support Desk" } as Application;
const entry = (displayName: string) => new TextEncoder().encode(JSON.stringify({ displayName, category: "productivity" }));

type Installed = { slug: string; enabled: boolean; version: number; configuration: Record<string, unknown>; redactedParameters: string[] };
type InstalledConnector = { name: string; description: string; required: boolean; status: string; installationVersion: number; provider: string; providerConnection: string; offered: boolean };
let installed: Installed[] = [];
let connectors: InstalledConnector[] = [];
const calls: Array<{ method: string; request: Record<string, unknown> }> = [];
const record = (method: string, request: Record<string, unknown>) => calls.push({ method, request });

const catalog = [
  { slug: "triage", source: "owned", version: "1", description: "Sorts tickets", nativeEntry: entry("Ticket triage"), skillCount: 2, agentCount: 1, mcpServerCount: 0, parameters: {} },
  { slug: "docs", source: "owned", version: "1", description: "Answers from docs", nativeEntry: entry("Docs answers"), skillCount: 1, agentCount: 0, mcpServerCount: 0,
    parameters: { docsUrl: { type: "string", title: "Documentation URL", description: "Where the docs live", required: true, sensitive: false, multiple: false, options: [], hasDefault: false } } },
  { slug: "talgent", source: "owned", version: "1", description: "Core", nativeEntry: entry("Talgent"), skillCount: 1, agentCount: 1, mcpServerCount: 0, parameters: {} },
];

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: "01a11fb2-e6c2-701f-80b7-38c54aa2eae9" } } }) }));
vi.mock("../lib/notifications", () => ({ notify: vi.fn() }));
vi.mock("../lib/client", () => ({
  marketplaceClient: {
    listPlugins: async () => ({ plugins: catalog, nextCursor: "" }),
    listInstalledPlugins: async (request: Record<string, unknown>) => { record("listInstalledPlugins", request); return { plugins: installed, nextCursor: "" }; },
    listInstalledConnectors: async (request: Record<string, unknown>) => { record("listInstalledConnectors", request); return { connectors }; },
    listConnectors: async (request: Record<string, unknown>) => {
      record("listConnectors", request);
      return { connectors: [{ name: "github", description: "Repositories", required: false, status: connectors.some(item => item.name === "github") ? "enabled" : "not_installed", installationVersion: 0, provider: "github", providerConnection: "not_connected", offered: true }] };
    },
    installPlugin: async (request: { slug: string; configuration: Record<string, unknown>; applicationId: string }) => {
      record("installPlugin", request);
      const plugin = { slug: request.slug, enabled: true, version: 1, configuration: request.configuration, redactedParameters: [] };
      installed = [...installed, plugin];
      return { plugin };
    },
    disablePlugin: async (request: { slug: string }) => {
      record("disablePlugin", request);
      installed = installed.map(item => item.slug === request.slug ? { ...item, enabled: false, version: item.version + 1 } : item);
      return {};
    },
    uninstallPlugin: async (request: { slug: string }) => { record("uninstallPlugin", request); installed = installed.filter(item => item.slug !== request.slug); return {}; },
    installConnector: async (request: { name: string }) => {
      record("installConnector", request);
      const connector = { name: request.name, description: "Repositories", required: false, status: "enabled", installationVersion: 1, provider: "github", providerConnection: "not_connected", offered: true };
      connectors = [...connectors, connector];
      return { connector };
    },
  },
}));

beforeEach(() => {
  installed = [{ slug: "triage", enabled: true, version: 1, configuration: {}, redactedParameters: [] }, { slug: "talgent", enabled: true, version: 1, configuration: {}, redactedParameters: [] }];
  connectors = [];
  calls.length = 0;
});

function renderSection() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  return render(<InstallationsSection application={application} required={new Set(["talgent"])} />, { wrapper });
}

it("lists the Application's own installations, not the person's, and switches one out of the default selection", async () => {
  const user = userEvent.setup();
  renderSection();
  const plugins = await screen.findByRole("list", { name: "Support Desk Plugins" });
  expect(within(plugins).getByText("Ticket triage")).toBeTruthy();
  expect(within(plugins).queryByText("Talgent"), "a Plugin every Work loads anyway is not an installation to manage").toBeNull();
  expect(calls.filter(call => call.method.startsWith("listInstalled")).every(call => call.request.applicationId === applicationId)).toBe(true);

  await user.click(screen.getByRole("switch", { name: "Load Ticket triage by default" }));
  await waitFor(() => expect(calls.find(call => call.method === "disablePlugin")?.request).toMatchObject({ slug: "triage", version: 1, applicationId }));
});

it("installs from the catalog for the Application, asking for settings a Plugin needs first", async () => {
  const user = userEvent.setup();
  renderSection();
  await screen.findByRole("list", { name: "Support Desk Plugins" });
  await user.click(screen.getByRole("button", { name: "Add Plugins" }));
  const dialog = await screen.findByRole("dialog", { name: "Add to Support Desk" });
  expect(within(dialog).getByLabelText("Ticket triage is installed")).toBeTruthy();
  expect(within(dialog).getByText("Always loads")).toBeTruthy();

  await user.click(within(dialog).getByRole("button", { name: "Install Docs answers" }));
  const settings = await screen.findByRole("dialog", { name: "Set up Docs answers" });
  await user.click(within(settings).getByRole("button", { name: "Install" }));
  expect(await within(settings).findByText("Enter documentation url.")).toBeTruthy();
  expect(calls.some(call => call.method === "installPlugin")).toBe(false);
  await user.type(within(settings).getByRole("textbox", { name: /Documentation URL/ }), "https://docs.example.com");
  await user.click(within(settings).getByRole("button", { name: "Install" }));
  await waitFor(() => expect(calls.find(call => call.method === "installPlugin")?.request).toMatchObject({ slug: "docs", applicationId, configuration: { docsUrl: "https://docs.example.com" } }));

  await user.click(within(dialog).getByRole("tab", { name: "Connectors" }));
  await user.click(await within(dialog).findByRole("button", { name: "Install GitHub" }));
  await waitFor(() => expect(calls.find(call => call.method === "installConnector")?.request).toMatchObject({ name: "github", applicationId }));
  expect(calls.find(call => call.method === "listConnectors")?.request).toMatchObject({ applicationId });
}, 15000);

it("removes an installation only after confirmation", async () => {
  const user = userEvent.setup();
  renderSection();
  await screen.findByRole("list", { name: "Support Desk Plugins" });
  await user.click(screen.getByRole("button", { name: "Remove Ticket triage" }));
  const confirm = await screen.findByRole("alertdialog", { name: "Remove Ticket triage from Support Desk?" });
  expect(calls.some(call => call.method === "uninstallPlugin")).toBe(false);
  await user.click(within(confirm).getByRole("button", { name: "Remove" }));
  await waitFor(() => expect(calls.find(call => call.method === "uninstallPlugin")?.request).toMatchObject({ slug: "triage", applicationId }));
});
