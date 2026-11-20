import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { EntryShellContext, personalPathsAt, type PageLayoutProps } from "../components/shell/entry-shell";
import { notify } from "../lib/notifications";
import { WorkshopPage } from "../pages/workshop-page";

const userId = "01a11fb2-e6c2-701f-80b7-38c54aa2eae9";
const crmId = "01900000-0000-7000-8000-000000000601";
const chromeId = "01900000-0000-7000-8000-000000000602";

type Stored = { connectorId: string; name: string; state: string; transport: string; provider: string; description: string; category: string; iconDigest: string; connectorVersion: number;
  publishedAtUnixMs: bigint; publisher: { kind: string; own: boolean; displayName: string }; settings: { url: string; command: string; arguments: string[] } };
let owned: Stored[] = [];
let icons: Record<string, { mediaType: string; data: Uint8Array }> = {};
const calls: Array<{ method: string; request: Record<string, unknown> }> = [];

const connectorOf = (connectorId: string, name: string, state: string, settings: Partial<Stored["settings"]>): Stored => ({
  connectorId, name, state, transport: settings.url ? "http" : "stdio", provider: "", description: `${name} tools`, category: "Sales", iconDigest: "", connectorVersion: 3,
  publishedAtUnixMs: state === "private" ? 0n : 1_700_000_000_000n, publisher: { kind: "person", own: true, displayName: "" }, settings: { url: "", command: "", arguments: [], ...settings },
});

// The Marketplace's answer to one owner command: the Connector at its next version.
function changed(request: { connectorId: string }, patch: (item: Stored) => Partial<Stored>) {
  owned = owned.map(item => item.connectorId === request.connectorId ? { ...item, ...patch(item), connectorVersion: item.connectorVersion + 1 } : item);
  return { connector: owned.find(item => item.connectorId === request.connectorId) };
}

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: userId } } }) }));
vi.mock("../lib/notifications", () => ({ notify: vi.fn() }));
vi.mock("../lib/client", () => ({
  marketplaceClient: {
    listProviders: async () => ({ providers: [] }),
    listConnectors: async () => ({ connectors: [] }),
    listOwnedConnectors: async () => ({ connectors: owned }),
    getConnectorIcon: async (request: { connectorId: string }) => ({ icon: icons[request.connectorId] }),
    changeConnector: async (request: Record<string, unknown> & { connectorId: string; settings: Stored["settings"]; description: string }) => {
      calls.push({ method: "changeConnector", request });
      return changed(request, () => ({ settings: request.settings, description: request.description }));
    },
    changeConnectorIcon: async (request: { connectorId: string; icon?: { mediaType: string; data: Uint8Array } }) => {
      calls.push({ method: "changeConnectorIcon", request });
      if (request.icon) icons[request.connectorId] = request.icon;
      else delete icons[request.connectorId];
      return changed(request, item => ({ iconDigest: request.icon ? `${item.connectorVersion}` : "" }));
    },
    withdrawConnector: async (request: { connectorId: string }) => { calls.push({ method: "withdrawConnector", request }); return changed(request, () => ({ state: "withdrawn" })); },
    deleteConnector: async (request: { connectorId: string }) => {
      calls.push({ method: "deleteConnector", request });
      owned = owned.filter(item => item.connectorId !== request.connectorId);
      return {};
    },
  },
}));

beforeEach(() => {
  owned = [connectorOf(crmId, "acme-crm", "published", { url: "https://crm.example.com/mcp" }),
    connectorOf(chromeId, "chrome-devtools", "private", { command: "npx", arguments: ["chrome-devtools-mcp@0.6.0", "--profile", "/home/me/My Profile"] })];
  icons = {};
  calls.length = 0;
  vi.mocked(notify).mockClear();
});

function Layout({ children }: PageLayoutProps) { return <main>{children}</main>; }
function Where() { return <p data-testid="location">{useLocation().pathname}</p>; }

function renderItem(connectorId: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>
    <MemoryRouter initialEntries={[`/workshop/connectors/${connectorId}`]}><EntryShellContext value={{ Layout, home: "/", personal: personalPathsAt("") }}>{children}<Where /></EntryShellContext></MemoryRouter>
  </QueryClientProvider>;
  return render(<Routes><Route path="/workshop/*" element={<WorkshopPage />} /></Routes>, { wrapper });
}

const toasts = () => vi.mocked(notify).mock.calls.map(call => call[2]);

it("shows a local command's arguments exactly and saves or discards changes from the bar", async () => {
  const user = userEvent.setup();
  renderItem(chromeId);
  const command = await screen.findByRole("textbox", { name: "Command" });
  expect(command).toHaveValue("npx chrome-devtools-mcp@0.6.0 --profile '/home/me/My Profile'");
  const bar = screen.getByRole("status", { hidden: true }).parentElement!;
  expect(bar).toHaveAttribute("aria-hidden", "true");

  const description = screen.getByRole("textbox", { name: "Description" });
  await user.click(description);
  await user.paste(" and more");
  expect(bar).toHaveAttribute("aria-hidden", "false");
  await user.click(within(bar).getByRole("button", { name: "Discard" }));
  expect(description).toHaveValue("chrome-devtools tools");
  expect(bar).toHaveAttribute("aria-hidden", "true");

  await user.clear(command);
  await user.paste(`npx chrome-devtools-mcp@0.6.0 --profile "/home/me/Work Profile"`);
  await user.click(within(bar).getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(calls.find(call => call.method === "changeConnector")?.request).toMatchObject({
    connectorId: chromeId, version: 3, settings: { url: "", command: "npx", arguments: ["chrome-devtools-mcp@0.6.0", "--profile", "/home/me/Work Profile"] } }));
  await waitFor(() => expect(toasts()).toContain("Changes saved"));
  await waitFor(() => expect(bar).toHaveAttribute("aria-hidden", "true"));
}, 3000);

it("uploads, replaces and removes the icon from the header, refusing a file that can't be one", async () => {
  const user = userEvent.setup({ applyAccept: false });
  const { container } = renderItem(crmId);
  await screen.findByRole("button", { name: "Change icon" });
  const input = container.querySelector<HTMLInputElement>("input[type=file]")!;
  await user.click(await screen.findByRole("button", { name: "Change icon" }));
  expect(await screen.findByRole("menuitem", { name: "Upload image…" })).toBeInTheDocument();
  expect(screen.queryByRole("menuitem", { name: "Remove image" })).toBeNull();
  await user.keyboard("{Escape}");

  await user.upload(input, new File(["%PDF"], "brochure.pdf", { type: "application/pdf" }));
  await waitFor(() => expect(toasts()).toContain("That file can’t be an icon"));
  expect(calls.some(call => call.method === "changeConnectorIcon")).toBe(false);

  await user.upload(input, new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "logo.png", { type: "image/png" }));
  await waitFor(() => expect(calls.at(-1)).toMatchObject({ method: "changeConnectorIcon", request: { connectorId: crmId, version: 3, icon: { mediaType: "image/png" } } }));
  await waitFor(() => expect(toasts()).toContain("Icon updated"));

  await user.click(screen.getByRole("button", { name: "Change icon" }));
  expect(await screen.findByRole("menuitem", { name: "Replace image…" })).toBeInTheDocument();
  await user.click(screen.getByRole("menuitem", { name: "Remove image" }));
  await waitFor(() => expect(calls.at(-1)).toMatchObject({ method: "changeConnectorIcon", request: { connectorId: crmId, version: 4, icon: undefined } }));
  await waitFor(() => expect(toasts()).toContain("Icon removed"));
}, 3000);

it("withdraws a published Connector only after confirmation", async () => {
  const user = userEvent.setup();
  renderItem(crmId);
  expect(screen.queryByRole("button", { name: "Delete…" })).toBeNull();
  await user.click(await screen.findByRole("button", { name: "Withdraw…" }));
  const dialog = await screen.findByRole("alertdialog");
  expect(within(dialog).getByText(/can’t publish it again/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
  expect(calls.some(call => call.method === "withdrawConnector")).toBe(false);

  await user.click(screen.getByRole("button", { name: "Withdraw…" }));
  await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Withdraw" }));
  await waitFor(() => expect(calls.find(call => call.method === "withdrawConnector")?.request).toMatchObject({ connectorId: crmId, version: 3 }));
  await waitFor(() => expect(toasts()).toContain("Withdrew acme-crm"));
  expect(await screen.findByText("You withdrew it")).toBeInTheDocument();
  expect(screen.queryByRole("textbox", { name: "Server address" })).toBeNull();
  // Its publication date belongs to it only while it is Published.
  expect(screen.queryByText(/^Published /)).toBeNull();
}, 3000);

it("deletes a private Connector only after confirmation and returns to the Workshop", async () => {
  const user = userEvent.setup();
  renderItem(chromeId);
  expect(screen.queryByRole("button", { name: "Withdraw…" })).toBeNull();
  await user.click(await screen.findByRole("button", { name: "Delete…" }));
  const dialog = await screen.findByRole("alertdialog");
  expect(within(dialog).getByText(/Your installation goes with it/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(calls.find(call => call.method === "deleteConnector")?.request).toMatchObject({ connectorId: chromeId, version: 3 }));
  await waitFor(() => expect(toasts()).toContain("Deleted chrome-devtools"));
  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(/^\/workshop$/));
}, 3000);
