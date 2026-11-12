import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { ClientSecretState } from "@talgent/contracts/account/public/v1/application_pb";
import { EntryShellContext, personalPathsAt, type PageLayoutProps } from "../components/shell/entry-shell";
import { applicationNameProblem } from "../lib/application-service";
import { ApplicationsPage } from "../pages/applications/applications-page";

const userId = "01a11fb2-e6c2-701f-80b7-38c54aa2eae9";
const deskId = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e";
const ciId = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4f";
const secret = "wbs_" + "A".repeat(39) + "k4Qz";
const rotatedSecret = "wbs_" + "B".repeat(39) + "Zr9x";

type Stored = { id: string; name: string; registeredAtUnixMs: bigint; secretOverlapSeconds: bigint; clientSecrets: Array<Record<string, unknown>> };
const stored: Stored[] = [];
// Makes the next removal fail as an unavailable server would.
let removalUnavailable = false;
const applicationOf = (id: string, name: string, lastUsed = 0n): Stored => ({
  id, name, registeredAtUnixMs: BigInt(Date.now()), secretOverlapSeconds: 24n * 3600n,
  clientSecrets: [{ id: `${id}-s`, state: ClientSecretState.CURRENT, hint: "k4Qz", issuedAtUnixMs: BigInt(Date.now()), lastUsedAtUnixMs: lastUsed, overlapEndsAtUnixMs: 0n }],
});

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: userId } } }) }));
vi.mock("../lib/notifications", () => ({ notify: vi.fn() }));

const agentId = "0193a7c2-6f1e-7c2a-9b3d-00000000a9e1";
type AgentState = { name: string; icon: { glyph: string; color: string }; soul: string; defaults: { startupAgent: string; modelConfigurationId: string; thinkingLevel: string } };
const agentState: AgentState = { name: "", icon: { glyph: "bot", color: "green" }, soul: "", defaults: { startupAgent: "", modelConfigurationId: "", thinkingLevel: "" } };
const agentCalls: Array<{ method: string; request: unknown }> = [];
const startOptions = {
  models: [
    { configurationId: "standard", name: "Standard", vendor: 1, thinkingLevels: ["low", "medium"], defaultThinkingLevel: "medium" },
    { configurationId: "fast", name: "Fast", vendor: 1, thinkingLevels: ["low", "high"], defaultThinkingLevel: "low" },
  ],
  plugins: [{ name: "support", displayName: "Support", agents: [{ name: "support:triage", description: "Sorts tickets" }], skills: [] }],
  connectors: [],
};
// The Application's usage as Observability answers it: Works with their Work
// Labels and output tokens, which a label key breaks down.
type MeasuredWork = { labels: Record<string, string>; output: number };
const measured: MeasuredWork[] = [];
const usageRequests: Array<{ applicationId: string; breakdownKey: string; listLabelKeys: boolean }> = [];
const totalsOf = (works: MeasuredWork[]) => works.length ? [{ metricKind: "output_tokens", dimension: "model", value: works.reduce((sum, work) => sum + work.output, 0), unknownCount: 0n }] : [];
vi.mock("../lib/client", () => ({
  traceClient: {
    getApplicationTraceMetrics: async (request: { applicationId: string; breakdownKey: string; listLabelKeys: boolean }) => {
      usageRequests.push(request);
      const key = request.breakdownKey;
      const groups = new Map<string, MeasuredWork[]>();
      for (const work of measured) {
        const group = key in work.labels ? `=${work.labels[key]}` : "";
        groups.set(group, [...(groups.get(group) ?? []), work]);
      }
      return {
        buckets: [],
        totals: totalsOf(measured),
        labelKeys: [...new Set(measured.flatMap(work => Object.keys(work.labels)))].sort(),
        breakdown: key ? [...groups].map(([group, works]) => ({ value: group.slice(1), labelled: group !== "", totals: totalsOf(works), workCount: works.length })) : [],
      };
    },
  },
  agentWorkClient: {
    ensureApplicationAgent: async (request: { applicationId: string }) => {
      agentCalls.push({ method: "ensureApplicationAgent", request });
      if (!agentState.name) agentState.name = stored.find(item => item.id === request.applicationId)?.name ?? "";
      return { agentId, appearance: { name: agentState.name, icon: agentState.icon } };
    },
    changeWorkAgentAppearance: async (request: { agentId: string; name: string; icon: { glyph: string; color: string } }) => {
      agentCalls.push({ method: "changeWorkAgentAppearance", request });
      agentState.name = request.name; agentState.icon = request.icon;
      return { appearance: { name: request.name, icon: request.icon } };
    },
    getAgentSoul: async () => ({ soul: agentState.soul }),
    changeAgentSoul: async (request: { agentId: string; soul: string }) => {
      agentCalls.push({ method: "changeAgentSoul", request });
      agentState.soul = request.soul;
      return { soul: request.soul };
    },
    getAgentWorkDefaults: async () => ({ defaults: { ...agentState.defaults }, options: startOptions }),
    changeAgentWorkDefaults: async (request: { agentId: string; defaults: AgentState["defaults"] }) => {
      agentCalls.push({ method: "changeAgentWorkDefaults", request });
      agentState.defaults = { ...request.defaults };
      return { defaults: request.defaults };
    },
  },
  applicationClient: {
    renameApplication: async ({ applicationId, name }: { applicationId: string; name: string }) => {
      const application = stored.find(item => item.id === applicationId)!;
      application.name = name;
      return { application };
    },
    listOwnedApplications: async () => ({ applications: [...stored] }),
    registerApplication: async ({ name }: { name: string }) => {
      const application = applicationOf(deskId, name);
      stored.push(application);
      return { application, clientSecret: secret };
    },
    rotateClientSecret: async ({ applicationId }: { applicationId: string }) => {
      const application = stored.find(item => item.id === applicationId)!;
      const now = BigInt(Date.now());
      application.clientSecrets = [
        { id: `${applicationId}-r`, state: ClientSecretState.CURRENT, hint: "Zr9x", issuedAtUnixMs: now, lastUsedAtUnixMs: 0n, overlapEndsAtUnixMs: 0n },
        ...application.clientSecrets.filter(item => item.state === ClientSecretState.CURRENT)
          .map(item => ({ ...item, state: ClientSecretState.OVERLAPPING, overlapEndsAtUnixMs: now + 24n * 3_600_000n })),
      ];
      return { application, clientSecret: rotatedSecret };
    },
    revokeClientSecret: async ({ applicationId, clientSecretId }: { applicationId: string; clientSecretId: string }) => {
      const application = stored.find(item => item.id === applicationId)!;
      if (!application.clientSecrets.some(item => item.id === clientSecretId)) throw new ConnectError("client secret not found", Code.NotFound);
      application.clientSecrets = application.clientSecrets.filter(item => item.id !== clientSecretId);
      return { application };
    },
    removeApplication: async ({ applicationId }: { applicationId: string }) => {
      if (removalUnavailable) throw new ConnectError("unavailable", Code.Unavailable);
      const index = stored.findIndex(item => item.id === applicationId);
      if (index < 0) throw new ConnectError("application not found", Code.NotFound);
      stored.splice(index, 1);
      return {};
    },
  },
}));

beforeEach(() => {
  stored.length = 0;
  measured.length = 0;
  usageRequests.length = 0;
  agentCalls.length = 0;
  removalUnavailable = false;
  Object.assign(agentState, { name: "", icon: { glyph: "bot", color: "green" }, soul: "", defaults: { startupAgent: "", modelConfigurationId: "", thinkingLevel: "" } });
});

// The frame a page renders in: its lead, tabs and crumbs, as an entry draws them.
function Layout({ children, lead, centerActions, trail = [], workspaceTitle }: PageLayoutProps) {
  return <div>
    <header>{lead}<nav aria-label="Page location">{trail.map(crumb => <span key={crumb.label}>{crumb.label}</span>)}{!lead && workspaceTitle}</nav>{centerActions}</header>
    <main>{children}</main>
  </div>;
}

function renderPage(path = "/applications") {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>
    <MemoryRouter initialEntries={[path]}><EntryShellContext value={{ Layout, home: "/", personal: personalPathsAt("") }}>{children}</EntryShellContext></MemoryRouter>
  </QueryClientProvider>;
  return render(<ApplicationsPage />, { wrapper });
}

it("registers a first application and shows its secret once, until the owner says they stored it", async () => {
  const user = userEvent.setup();
  renderPage();

  await user.click(await screen.findByRole("button", { name: "Register application" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "Register" }));
  expect(within(dialog).getByText("Enter a name.")).toBeInTheDocument();
  await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Support Desk");
  await user.click(within(dialog).getByRole("button", { name: "Register" }));

  expect(await within(dialog).findByRole("heading", { name: "Support Desk is registered" })).toBeInTheDocument();
  expect(within(dialog).getByText(deskId)).toBeInTheDocument();
  expect(within(dialog).getByText(secret)).toBeInTheDocument();
  const done = within(dialog).getByRole("button", { name: "Done" });
  expect(done).toBeDisabled();
  await user.click(within(dialog).getByRole("checkbox", { name: "I stored the secret" }));
  await user.click(done);

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(screen.queryByText(secret)).not.toBeInTheDocument();
  const credentials = await screen.findByRole("list", { name: "Support Desk credentials" });
  expect(within(credentials).getByText(deskId)).toBeInTheDocument();
  expect(within(credentials).getByText("wbs_…k4Qz")).toBeInTheDocument();
  expect(within(credentials).getByText(/never used/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Application menu: Support Desk" })).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "Credentials" })).toHaveAttribute("aria-selected", "true");
});

it("lists the owner's applications in the switcher and opens the one in ?app=", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"), applicationOf(ciId, "CI Triage", BigInt(Date.now() - 2 * 3_600_000)));
  renderPage(`/applications?app=${ciId}`);

  const credentials = await screen.findByRole("list", { name: "CI Triage credentials" });
  expect(within(credentials).getByText(/last used 2h ago/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Application menu: CI Triage" }));
  const menu = await screen.findByRole("menu");
  expect(within(menu).getAllByRole("menuitem").map(item => item.textContent)).toEqual(["Support Desk", "CI Triage", "Register application"]);
  await user.click(within(menu).getByRole("menuitem", { name: "Support Desk" }));
  expect(await screen.findByRole("list", { name: "Support Desk credentials" })).toBeInTheDocument();
});

it("rotates a secret, shows the new one once and keeps the replaced one overlapping", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  renderPage();

  await screen.findByRole("list", { name: "Support Desk credentials" });
  expect(screen.getByText("Rotating keeps the replaced secret working for 24 hours.")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Rotate secret" }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText(/keeps working for 24 hours, then ends/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Rotate secret" }));
  expect(await within(dialog).findByRole("heading", { name: "New secret issued" })).toBeInTheDocument();
  expect(within(dialog).getByText(rotatedSecret)).toBeInTheDocument();
  expect(within(dialog).getByText(/previous secret keeps working until/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("checkbox", { name: "I stored the secret" }));
  await user.click(within(dialog).getByRole("button", { name: "Done" }));

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  const credentials = screen.getByRole("list", { name: "Support Desk credentials" });
  expect(within(credentials).getByText("wbs_…Zr9x")).toBeInTheDocument();
  expect(within(credentials).getByText("Overlapping")).toBeInTheDocument();
  expect(within(credentials).getByText(/ends in 2[34]h/)).toBeInTheDocument();
  expect(screen.queryByText(rotatedSecret)).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Rotate secret" }));
  expect(await within(await screen.findByRole("dialog")).findByText(/Rotating again ends it now/)).toBeInTheDocument();
});

it("revokes a secret after confirmation and warns when it is the last one", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  renderPage();

  await user.click(await screen.findByRole("button", { name: "Revoke secret wbs_…k4Qz" }));
  const dialog = await screen.findByRole("alertdialog");
  expect(within(dialog).getByText(/only secret/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Revoke secret" }));

  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  expect(await screen.findByText("No secret")).toBeInTheDocument();
});

it("hides an overlap that ended since the list was read", async () => {
  const application = applicationOf(deskId, "Support Desk");
  application.clientSecrets.push({ id: "ended", state: ClientSecretState.OVERLAPPING, hint: "Old1", issuedAtUnixMs: 0n, lastUsedAtUnixMs: 0n, overlapEndsAtUnixMs: BigInt(Date.now() - 1000) });
  stored.push(application);
  renderPage();

  const credentials = await screen.findByRole("list", { name: "Support Desk credentials" });
  expect(within(credentials).getByText("wbs_…k4Qz")).toBeInTheDocument();
  expect(within(credentials).queryByText("wbs_…Old1")).not.toBeInTheDocument();
});

it("explains a secret that ended before the revocation and rereads the list", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  renderPage();

  await user.click(await screen.findByRole("button", { name: "Revoke secret wbs_…k4Qz" }));
  stored[0].clientSecrets = [];
  const dialog = await screen.findByRole("alertdialog");
  await user.click(within(dialog).getByRole("button", { name: "Revoke secret" }));

  expect(await within(dialog).findByText("The secret couldn’t be revoked")).toBeInTheDocument();
  expect(await screen.findByText("No secret")).toBeInTheDocument();
});

it("removes an application only after its final confirmation, then shows the one left", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"), applicationOf(ciId, "CI Triage"));
  renderPage(`/applications?app=${ciId}&section=settings`);

  await user.click(await screen.findByRole("button", { name: "Remove application" }));
  let dialog = await screen.findByRole("alertdialog");
  expect(within(dialog).getByRole("heading", { name: "Remove CI Triage?" })).toBeInTheDocument();
  expect(within(dialog).getByText(/can’t restore it/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  expect(stored.map(item => item.name)).toEqual(["Support Desk", "CI Triage"]);

  await user.click(screen.getByRole("button", { name: "Remove application" }));
  dialog = await screen.findByRole("alertdialog");
  await user.click(within(dialog).getByRole("button", { name: "Remove application" }));

  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  expect(stored.map(item => item.name)).toEqual(["Support Desk"]);
  expect(await screen.findByRole("list", { name: "Support Desk credentials" })).toBeInTheDocument();
});

it("rereads the list when the application was removed meanwhile", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  renderPage("/applications?section=settings");

  await user.click(await screen.findByRole("button", { name: "Remove application" }));
  stored.length = 0;
  const dialog = await screen.findByRole("alertdialog");
  await user.click(within(dialog).getByRole("button", { name: "Remove application" }));

  expect(await screen.findByText("No applications yet")).toBeInTheDocument();
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
});

it("leaves the settings of an application removed meanwhile for the one left", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"), applicationOf(ciId, "CI Triage"));
  renderPage(`/applications?app=${ciId}&section=settings`);

  await user.click(await screen.findByRole("button", { name: "Remove application" }));
  stored.splice(1, 1);
  const dialog = await screen.findByRole("alertdialog");
  await user.click(within(dialog).getByRole("button", { name: "Remove application" }));

  expect(await screen.findByRole("list", { name: "Support Desk credentials" })).toBeInTheDocument();
});

it("keeps the confirmation open and explains a removal that failed", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  renderPage("/applications?section=settings");

  await user.click(await screen.findByRole("button", { name: "Remove application" }));
  const dialog = await screen.findByRole("alertdialog");
  removalUnavailable = true;
  await user.click(within(dialog).getByRole("button", { name: "Remove application" }));

  expect(await within(dialog).findByText("The application couldn’t be removed")).toBeInTheDocument();
  expect(within(dialog).getByText("Try again in a moment.")).toBeInTheDocument();
  expect(stored.map(item => item.name)).toEqual(["Support Desk"]);
});

it("explains an empty list and invites a registration", async () => {
  renderPage();
  expect(await screen.findByText("No applications yet")).toBeInTheDocument();
  expect(screen.queryByRole("tab")).not.toBeInTheDocument();
});

it("bounds a name to 60 characters after trimming", () => {
  expect(applicationNameProblem("   ")).toBe("Enter a name.");
  expect(applicationNameProblem(` ${"x".repeat(60)} `)).toBe("");
  expect(applicationNameProblem("x".repeat(61))).toBe("Use at most 60 characters.");
});

it("configures the application's Agent: its appearance, its Soul and its Work defaults", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  renderPage(`/applications?app=${deskId}&section=agent`);

  const appearance = await screen.findByRole("form", { name: "Support Desk’s Agent" });
  const name = within(appearance).getByRole("textbox", { name: "Name" });
  expect(name).toHaveValue("Support Desk");
  expect(within(appearance).getByText("Every Work of Support Desk runs as this Agent and shares its memory.")).toBeInTheDocument();
  await user.clear(name);
  await user.type(name, "Desk Bot");
  await user.click(within(appearance).getByRole("radio", { name: "Rocket" }));
  await user.click(within(appearance).getByRole("button", { name: "Save" }));
  await waitFor(() => expect(agentCalls).toContainEqual({ method: "changeWorkAgentAppearance", request: { agentId, name: "Desk Bot", icon: { glyph: "rocket", color: "green" } } }));

  const soul = await screen.findByRole("form", { name: "Soul" });
  await user.type(within(soul).getByRole("textbox", { name: "Soul" }), "Answer customers kindly.");
  await user.click(within(soul).getByRole("button", { name: "Save" }));
  await waitFor(() => expect(agentCalls).toContainEqual({ method: "changeAgentSoul", request: { agentId, soul: "Answer customers kindly." } }));

  const defaults = await screen.findByRole("list", { name: "Work defaults" });
  expect(within(defaults).getByRole("button", { name: "Model and thinking effort" })).toHaveTextContent("Standard");
  await user.click(within(defaults).getByRole("button", { name: "Model and thinking effort" }));
  await user.click(await screen.findByRole("option", { name: /Fast/ }));
  await waitFor(() => expect(agentCalls).toContainEqual({ method: "changeAgentWorkDefaults", request: { agentId, defaults: { startupAgent: "", modelConfigurationId: "fast", thinkingLevel: "low" } } }));
});

it("chooses the Startup Agent from the Plugins the application's Works load, and warns about one none declares", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  agentState.defaults = { startupAgent: "retired:helper", modelConfigurationId: "", thinkingLevel: "" };
  renderPage(`/applications?app=${deskId}&section=agent`);

  expect(await screen.findByText(/helper isn’t declared by any Plugin its Works load/)).toBeInTheDocument();
  const defaults = screen.getByRole("list", { name: "Work defaults" });
  await user.click(within(defaults).getByRole("button", { name: /Startup Agent/ }));
  await user.click(await screen.findByRole("option", { name: /triage/ }));
  await waitFor(() => expect(agentCalls).toContainEqual({ method: "changeAgentWorkDefaults", request: { agentId, defaults: { startupAgent: "support:triage", modelConfigurationId: "", thinkingLevel: "" } } }));
  await waitFor(() => expect(screen.queryByText(/isn’t declared by any Plugin/)).not.toBeInTheDocument());
});

it("renames the application in its settings", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  renderPage(`/applications?app=${deskId}&section=settings`);

  const form = await screen.findByRole("form", { name: "Application name" });
  const name = within(form).getByRole("textbox", { name: "Name" });
  await user.clear(name);
  await user.click(within(form).getByRole("button", { name: "Save" }));
  expect(within(form).getByText("Enter a name.")).toBeInTheDocument();
  await user.type(name, "  Help Center ");
  await user.click(within(form).getByRole("button", { name: "Save" }));
  expect(await screen.findByRole("button", { name: "Application menu: Help Center" })).toBeInTheDocument();
});

it("shows the application's tokens and secret state, and breaks its usage down by a Work Label key", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"));
  measured.push(
    { labels: { tenant: "acme", user: "u1" }, output: 1500 },
    { labels: { tenant: "acme", user: "u2" }, output: 500 },
    { labels: { user: "u3" }, output: 250 },
  );
  renderPage(`/applications?app=${deskId}&section=overview`);

  const glance = await screen.findByRole("group", { name: "Support Desk at a glance" });
  expect(await within(glance).findByText("2.3k")).toBeInTheDocument();
  expect(within(glance).getByText("Input 0 · Cache read 0 · Cache write 0 · Output 2.3k")).toBeInTheDocument();
  expect(within(glance).getByText("1 current")).toBeInTheDocument();

  const byTenant = await screen.findByRole("list", { name: "Usage by tenant" });
  const rows = within(byTenant).getAllByRole("listitem");
  expect(rows.map(row => row.textContent)).toEqual([
    expect.stringContaining("tenant=acme2k · 2 Works"),
    expect.stringContaining("No tenant250 · 1 Work"),
  ]);
  expect(usageRequests.every(request => request.applicationId === deskId && request.listLabelKeys)).toBe(true);

  await user.click(screen.getByRole("button", { name: /By tenant/ }));
  await user.click(await screen.findByRole("option", { name: "user" }));
  const byUser = await screen.findByRole("list", { name: "Usage by user" });
  expect(within(byUser).getAllByRole("listitem")).toHaveLength(3);

  await user.click(within(glance).getByRole("button", { name: "Secrets: 1 current" }));
  expect(await screen.findByRole("list", { name: "Support Desk credentials" })).toBeInTheDocument();
});

it("explains an application with no usage, and one whose Works carry no labels", async () => {
  stored.push(applicationOf(deskId, "Support Desk"), applicationOf(ciId, "CI Triage"));
  stored[1].clientSecrets = [];
  renderPage(`/applications?app=${deskId}&section=overview`);
  expect(await screen.findByText("Usage appears once Support Desk runs Works.")).toBeInTheDocument();

  measured.push({ labels: {}, output: 10 });
  renderPage(`/applications?app=${ciId}&section=overview`);
  expect(await screen.findByText("CI Triage’s Works carry no Work Labels to break their usage down by.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Secrets: None" })).toBeInTheDocument();
});
