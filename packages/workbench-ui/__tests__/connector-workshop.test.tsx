import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import type { DeclaredProvider } from "@talgent/contracts/marketplace/public/v1/connections_pb";
import type { WorkStartConnector } from "@talgent/contracts/agent/public/v1/work_start_options_pb";
import { EntryShellContext, personalPathsAt, type PageLayoutProps } from "../components/shell/entry-shell";
import { draftProblems, emptyDraft } from "../lib/connector-workshop";
import { connectorChoices, connectorLoadout } from "../lib/work-start-loadout";
import { WorkshopPage } from "../pages/workshop-page";

const userId = "01a11fb2-e6c2-701f-80b7-38c54aa2eae9";
const github = { name: "github", displayName: "GitHub", targets: [{ name: "github-api", hosts: ["api.github.com", "*.github.example.com"] }] } as unknown as DeclaredProvider;

type Stored = { connectorId: string; name: string; state: string; transport: string; provider: string; description: string; category: string; iconDigest: string; connectorVersion: number;
  publishedAtUnixMs: bigint; publisher: { kind: string; own: boolean; displayName: string }; settings: { url: string; command: string; arguments: string[] } };
let owned: Stored[] = [];
const calls: Array<{ method: string; request: Record<string, unknown> }> = [];

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: userId } } }) }));
vi.mock("../lib/notifications", () => ({ notify: vi.fn() }));
vi.mock("../lib/client", () => ({
  marketplaceClient: {
    listProviders: async () => ({ providers: [github] }),
    listConnectors: async () => ({ connectors: [] }),
    listOwnedConnectors: async () => ({ connectors: owned }),
    addConnector: async (request: Record<string, unknown> & { name: string; settings: Stored["settings"]; transport: string }) => {
      calls.push({ method: "addConnector", request });
      const connector: Stored = { connectorId: "01900000-0000-7000-8000-000000000501", name: request.name, state: "private", transport: request.transport, provider: String(request.provider),
        description: String(request.description), category: String(request.category), iconDigest: "", connectorVersion: 1, publishedAtUnixMs: 0n,
        publisher: { kind: "person", own: true, displayName: "" }, settings: request.settings };
      owned = [...owned, connector];
      return { connector };
    },
    publishConnector: async (request: { connectorId: string; version: number }) => {
      calls.push({ method: "publishConnector", request });
      owned = owned.map(item => item.connectorId === request.connectorId ? { ...item, state: "published", connectorVersion: item.connectorVersion + 1, publishedAtUnixMs: BigInt(Date.now()) } : item);
      return { connector: owned.find(item => item.connectorId === request.connectorId) };
    },
  },
}));

beforeEach(() => { owned = []; calls.length = 0; });

function Layout({ children }: PageLayoutProps) { return <main>{children}</main>; }

function renderWorkshop(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>
    <MemoryRouter initialEntries={[path]}><EntryShellContext value={{ Layout, home: "/", personal: personalPathsAt("") }}>{children}</EntryShellContext></MemoryRouter>
  </QueryClientProvider>;
  return render(<Routes><Route path="/workshop/*" element={<WorkshopPage />} /></Routes>, { wrapper });
}

it("checks a draft as the Marketplace would before asking it", () => {
  const options = { adding: true, providers: [github], takenNames: ["acme-crm"] };
  expect(draftProblems({ ...emptyDraft, name: "Acme CRM", url: "https://crm.example.com/mcp" }, options).name).toMatch(/lowercase/);
  expect(draftProblems({ ...emptyDraft, name: "acme-crm", url: "https://crm.example.com/mcp" }, options).name).toMatch(/already have/);
  expect(draftProblems({ ...emptyDraft, name: "gh", provider: "github", url: "https://evil.example.com/mcp" }, options).url).toMatch(/only reach api.github.com/);
  expect(draftProblems({ ...emptyDraft, name: "gh", provider: "github", url: "https://mcp.github.example.com/mcp" }, options)).toEqual({});
  expect(draftProblems({ ...emptyDraft, name: "gh", provider: "github", url: "https://mcp.github.example.com:8443/mcp" }, options).url).toBeTruthy();
  expect(draftProblems({ ...emptyDraft, name: "chrome", transport: "stdio", command: " " }, options).command).toBeTruthy();
  expect(draftProblems({ ...emptyDraft, name: "chrome", transport: "stdio", command: "node 'server" }, options).command).toBe("Close the single quote.");
});

it("marks two included Connectors of one name and chooses them by identity", () => {
  const option = (connectorId: string, publisher: string, includedByDefault: boolean) => ({ connectorId, name: "chrome-devtools", publisher, includedByDefault, required: false, unavailableOnManaged: false }) as unknown as WorkStartConnector;
  const options = [option("c-mine", "", true), option("c-theirs", "Alex Chen", false)];
  const draft = { connectors: { "c-theirs": true } } as unknown as Parameters<typeof connectorLoadout>[2];
  const rows = connectorLoadout(options, [], draft);
  expect(rows.map(row => row.clashes)).toEqual([true, true]);
  expect(connectorChoices(rows)).toEqual([{ connectorId: "c-theirs", include: true }]);
  const resolved = connectorLoadout(options, [], { ...draft, connectors: { "c-theirs": true, "c-mine": false } });
  expect(resolved.map(row => row.clashes)).toEqual([false, false]);
});

it("adds a private Connector and opens its page", async () => {
  const user = userEvent.setup();
  renderWorkshop("/workshop");
  await user.click(await screen.findByRole("button", { name: /Add/ }));
  await user.click(await screen.findByRole("menuitem", { name: /Connector/ }));

  await user.click(await screen.findByRole("textbox", { name: "Name" }));
  await user.paste("acme-crm");
  await user.click(screen.getByRole("textbox", { name: "Server address" }));
  await user.paste("https://crm.example.com/mcp");
  await user.click(screen.getByRole("button", { name: "Add connector" }));
  await waitFor(() => expect(calls.find(call => call.method === "addConnector")?.request).toMatchObject({ name: "acme-crm", transport: "http", provider: "", settings: { url: "https://crm.example.com/mcp" } }));
  // Adding opens the added Connector's own page, Private to its owner.
  expect(await screen.findByText("Private")).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "acme-crm" })).toBeInTheDocument();
}, 3000);

it("publishes a private Connector after confirmation", async () => {
  const user = userEvent.setup();
  owned = [{ connectorId: "01900000-0000-7000-8000-000000000502", name: "acme-crm", state: "private", transport: "http", provider: "", description: "", category: "", iconDigest: "",
    connectorVersion: 1, publishedAtUnixMs: 0n, publisher: { kind: "person", own: true, displayName: "" }, settings: { url: "https://crm.example.com/mcp", command: "", arguments: [] } }];
  renderWorkshop(`/workshop/connectors/${owned[0].connectorId}`);
  await user.click(await screen.findByRole("button", { name: "Publish…" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "Publish" }));
  await waitFor(() => expect(calls.find(call => call.method === "publishConnector")?.request).toMatchObject({ connectorId: owned[0].connectorId, version: 1 }));
  expect(await screen.findByText("Published")).toBeInTheDocument();
  expect(await screen.findByRole("button", { name: "Withdraw…" })).toBeInTheDocument();
}, 3000);
