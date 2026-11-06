import { Code, ConnectError, type HandlerContext } from "@connectrpc/connect";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ModelVendor } from "@talgent/workbench-contracts/agent/public/v1/model_pb";
import { PermissionMode } from "@talgent/workbench-contracts/agent/public/v1/harness_pb";
import { UsableComputerKind } from "@talgent/workbench-contracts/computer/public/v1/computer_pb";
import { WorkFileKind } from "@talgent/workbench-contracts/agent/public/v1/work_file_pb";
import { personalPathsAt } from "@talgent/workbench-ui/components/shell/entry-shell";
import WorkbenchRoutes from "../workbench-routes";
import { recorder, resetServices, services, userId } from "@talgent/workbench-ui/test-support/backend";

vi.mock("@talgent/workbench-ui/lib/transport", async () => (await import("@talgent/workbench-ui/test-support/backend")).transportModule);

const minutesAgo = (minutes: number) => timestampFromDate(new Date(Date.now() - minutes * 60_000));
const works = [
  { workId: "pw-1", title: "Draft this week's status report", state: "working", waiting: false, updatedAt: minutesAgo(3) },
  { workId: "pw-2", title: "Compare the two vendor contracts", state: "stopped", waiting: true, updatedAt: minutesAgo(42) },
];
let shareReaderIds: string[];

function Location() {
  const location = useLocation();
  return <output aria-label="Current location">{location.pathname}{location.search}</output>;
}

async function renderWorkbench(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}><WorkbenchRoutes /><Location /></MemoryRouter></QueryClientProvider>));
  return client;
}
const location = () => screen.getByLabelText("Current location");

// The app graph is imported once; a cold import under a parallel suite can
// take longer than one test's timeout.
beforeAll(async () => { await import("../workbench-routes"); }, 60_000);

beforeEach(() => {
  resetServices();
  shareReaderIds = [];
  Object.assign(services.personal, {
    listPersonalWorks: () => ({ works }),
    getPersonalWorkStartOptions: () => ({ options: { models: [{ configurationId: "deepseek-pro", name: "deepseek-v4-pro", vendor: ModelVendor.DEEPSEEK, thinkingLevels: ["high"], defaultThinkingLevel: "high" }], plugins: [], connectors: [] } }),
    listPersonalWorkStartRepositories: () => ({ repositories: { repositories: [], recentRepositoryIds: [] } }),
    getShareReaders: (request: { userIds: string[] }) => ({ users: request.userIds.map(id => ({ userId: id, username: id === "lin" ? "lin" : "alex", displayName: id === "lin" ? "Lin Chen" : "Alex Kim" })) }),
    listLibraryFiles: () => ({ files: [], works: [] }),
  });
  Object.assign(services.marketplace, { listPlugins: () => ({ plugins: [] }), listInstalledPlugins: () => ({ plugins: [] }), listConnectors: () => ({ connectors: [] }), listInstalledConnectors: () => ({ connectors: [] }) });
  Object.assign(services.agent, {
    getWork: (request: { workId: string }) => {
      if (request.workId === "shared-1") throw new ConnectError("not the owner", Code.PermissionDenied);
      return { work: { workId: request.workId, state: "working", allowedActions: ["stop"], version: 1n, shareReaderIds, startupProgress: { status: "completed" } } };
    },
    getCurrentTurn: () => ({ turnId: "", active: false }),
    listPendingInteractions: () => ({ interactions: [] }),
    getWorkCapabilities: () => ({ observed: false, commands: [] }),
    getWorkPluginResults: () => ({ plugins: [] }),
  });
  Object.assign(services.workFile, { listWorkFiles: () => ({ files: [] }) });
  Object.assign(services.computer, { listUsableComputers: () => ({ computers: [{ id: "cloud-1", name: "Cloud", kind: UsableComputerKind.CLOUD, reachable: true }] }) });
  Object.assign(services.stream, {
    listMessagesBySession: () => ({ messages: [], hasMore: false }),
    watchStream: async function* (request: { agentWorkId?: string }, context: HandlerContext) {
      yield { payload: { case: "workState", value: { agentWorkId: request.agentWorkId, state: "working", subagents: [] } } };
      await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
    },
  });
  Object.assign(services.trace, { getSessionTraceMetrics: () => ({ totals: [], buckets: [] }), getPersonalTraceMetrics: () => ({ totals: [], buckets: [] }) });
});

describe("personal pages", () => {
  it("sit at the root or under the /personal base an app mounts them at", () => {
    expect(personalPathsAt("")).toMatchObject({ newWork: "/", works: "/works", library: "/library", usage: "/usage" });
    expect(personalPathsAt("/personal")).toMatchObject({ newWork: "/personal/new", works: "/personal/works", library: "/personal/library", usage: "/personal/usage" });
    expect(personalPathsAt("/personal").work("a b")).toBe("/personal/works/a%20b");
  });
});

describe("New work", () => {
  it("starts a Personal Work with no Subject and opens it, keeping one request identity across retries", async () => {
    let attempts = 0;
    const start = recorder(() => { attempts += 1; if (attempts === 1) throw new ConnectError("busy", Code.Unavailable); return { workId: "pw-9" }; });
    services.personal.startPersonalWork = start;
    const user = userEvent.setup();
    await renderWorkbench("/");
    await user.type(await screen.findByLabelText("Initial instruction"), "Summarize the interviews");
    await waitFor(() => expect(screen.getByRole("button", { name: "Start work" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "Start work" }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(location()).toHaveTextContent("/works/pw-9"));
    const [first, second] = start.calls as Array<Record<string, unknown>>;
    expect(first).toMatchObject({ initialMessage: "Summarize the interviews", model: { configurationId: "deepseek-pro" }, thinkingLevel: "high" });
    expect(first.requestId).toBeTruthy();
    expect(second.requestId).toBe(first.requestId);
  });
});

describe("New work Computer", () => {
  const cloud = { id: "cloud-1", name: "Cloud", kind: UsableComputerKind.CLOUD, reachable: true };
  const studio = { id: "mc-1", name: "Studio Mac", kind: UsableComputerKind.MANAGED, reachable: true };
  async function startOn(computers: unknown[]) {
    const start = recorder(() => ({ workId: "pw-9" }));
    services.personal.startPersonalWork = start;
    services.computer.listUsableComputers = () => ({ computers });
    const user = userEvent.setup();
    const client = await renderWorkbench("/");
    await user.type(await screen.findByLabelText("Initial instruction"), "Summarize the interviews");
    await waitFor(() => expect(screen.getByRole("button", { name: "Start work" })).toBeEnabled());
    return { user, start, client };
  }

  it("states the one Cloud and runs there when it is the only choice", async () => {
    const { user, start } = await startOn([cloud]);
    const chip = await screen.findByRole("button", { name: "Computer: Cloud" });
    await user.click(chip);
    expect(within(await screen.findByRole("listbox", { name: "Computer" })).getAllByRole("option")).toHaveLength(1);
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(start.calls).toEqual([expect.objectContaining({ computerId: "cloud-1" })]));
  });

  it("lists every Cloud and the person's Managed Computers, the offline one unpickable, and starts on the chosen one", async () => {
    const { user, start } = await startOn([cloud,
      { id: "mc-1", name: "Studio Mac", kind: UsableComputerKind.MANAGED, reachable: true },
      { id: "mc-2", name: "Old laptop", kind: UsableComputerKind.MANAGED, reachable: false }]);
    await user.click(await screen.findByRole("button", { name: "Computer: Cloud" }));
    const list = await screen.findByRole("listbox", { name: "Computer" });
    expect(within(list).getAllByRole("option").map(option => option.textContent)).toEqual(["CloudManaged by Talgent", "Studio MacManaged Computer", "Old laptopManaged ComputerOffline"]);
    expect(within(list).getByRole("option", { name: /Old laptop/ })).toHaveAttribute("aria-disabled", "true");
    await user.click(within(list).getByRole("option", { name: /Studio Mac/ }));
    expect(await screen.findByRole("button", { name: "Computer: Studio Mac" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(start.calls).toEqual([expect.objectContaining({ computerId: "mc-1" })]));
  });

  it("never starts a remembered Managed choice elsewhere while the Computers cannot confirm it, and retries their load", async () => {
    const { user, start, client } = await startOn([cloud, studio]);
    await user.click(await screen.findByRole("button", { name: "Computer: Cloud" }));
    await user.click(within(await screen.findByRole("listbox", { name: "Computer" })).getByRole("option", { name: /Studio Mac/ }));
    services.computer.listUsableComputers = () => { throw new ConnectError("down", Code.Unavailable); };
    await act(async () => { void client.resetQueries({ queryKey: ["usable-computers"] }); });
    await user.click(screen.getByRole("button", { name: "Start work" }));
    const failure = await screen.findByText("Computers could not be loaded.");
    await user.click(screen.getByRole("button", { name: "Start work" }));
    expect(start.calls).toHaveLength(0);
    services.computer.listUsableComputers = () => ({ computers: [cloud, studio] });
    await user.click(within(failure).getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("button", { name: "Computer: Studio Mac" })).toBeInTheDocument();
    expect(screen.queryByText("Computers could not be loaded.")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(start.calls).toEqual([expect.objectContaining({ computerId: "mc-1" })]));
  });

  it("asks to choose again when the remembered Computer is gone from the list", async () => {
    const { user, start, client } = await startOn([cloud, studio]);
    await user.click(await screen.findByRole("button", { name: "Computer: Cloud" }));
    await user.click(within(await screen.findByRole("listbox", { name: "Computer" })).getByRole("option", { name: /Studio Mac/ }));
    services.computer.listUsableComputers = () => ({ computers: [cloud] });
    await act(async () => { void client.refetchQueries({ queryKey: ["usable-computers"] }); });
    const notice = await screen.findByText("The Computer you chose is no longer available.");
    expect(screen.getByRole("button", { name: "Computer: Choose a Computer" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start work" }));
    expect(await screen.findByText("Choose a Computer before starting.")).toBeInTheDocument();
    expect(start.calls).toHaveLength(0);
    await user.click(within(notice).getByRole("button", { name: "Choose a Computer" }));
    await user.click(within(await screen.findByRole("listbox", { name: "Computer" })).getByRole("option", { name: /Cloud/ }));
    expect(screen.queryByText("The Computer you chose is no longer available.")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(start.calls).toEqual([expect.objectContaining({ computerId: "cloud-1" })]));
  });

  it("starts on a Managed Computer with the machine's own Claude login, every thinking level offered and none chosen", async () => {
    const { user, start } = await startOn([cloud, studio]);
    expect(await screen.findByRole("button", { name: "Model and thinking effort" })).toHaveTextContent("deepseek-v4-pro");
    await user.click(await screen.findByRole("button", { name: "Computer: Cloud" }));
    await user.click(within(await screen.findByRole("listbox", { name: "Computer" })).getByRole("option", { name: /Studio Mac/ }));
    const trigger = screen.getByRole("button", { name: "Model and thinking effort" });
    expect(trigger).toHaveTextContent("This machine's Claude login");
    await user.click(trigger);
    expect(within(await screen.findByRole("listbox", { name: "Models" })).getAllByRole("option").map(option => option.textContent)).toEqual(["This machine's Claude loginAnthropic", "deepseek-v4-proDeepSeek"]);
    expect(within(screen.getByRole("radiogroup", { name: "Thinking effort" })).getAllByRole("radio").map(level => level.textContent)).toEqual(["Low", "Medium", "High", "Xhigh", "Max"]);
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(start.calls).toEqual([expect.objectContaining({ computerId: "mc-1", model: expect.objectContaining({ machineLogin: true, configurationId: "" }), thinkingLevel: "" })]));
  });

  it("starts on a Managed Computer with a provided Model Configuration", async () => {
    const { user, start } = await startOn([cloud, studio]);
    await user.click(await screen.findByRole("button", { name: "Computer: Cloud" }));
    await user.click(within(await screen.findByRole("listbox", { name: "Computer" })).getByRole("option", { name: /Studio Mac/ }));
    await user.click(screen.getByRole("button", { name: "Model and thinking effort" }));
    await user.click(within(await screen.findByRole("listbox", { name: "Models" })).getByRole("option", { name: /deepseek-v4-pro/ }));
    await user.keyboard("{Escape}");
    expect(screen.getByRole("button", { name: "Model and thinking effort" })).toHaveTextContent("deepseek-v4-pro");
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(start.calls).toEqual([expect.objectContaining({ computerId: "mc-1", model: expect.objectContaining({ configurationId: "deepseek-pro", machineLogin: false }), thinkingLevel: "high" })]));
  });

  it("shows a Provider-authorized Connector as unavailable on a Managed Computer and does not send it", async () => {
    const startOptions = services.personal.getPersonalWorkStartOptions as () => { options: Record<string, unknown> };
    services.personal.getPersonalWorkStartOptions = () => ({ options: { ...startOptions().options, connectors: [{ name: "github", includedByDefault: true, unavailableOnManaged: true }, { name: "linear", required: true, includedByDefault: true, unavailableOnManaged: true }, { name: "talgent", required: true, includedByDefault: true }] } });
    services.marketplace.listConnectors = () => ({ connectors: [{ name: "github", description: "Repositories.", offered: true, status: "enabled", provider: "github", providerConnection: "connected" }] });
    const { user, start } = await startOn([cloud, studio]);
    await user.click(await screen.findByRole("button", { name: "Computer: Cloud" }));
    await user.click(within(await screen.findByRole("listbox", { name: "Computer" })).getByRole("option", { name: /Studio Mac/ }));
    await user.click(screen.getByRole("button", { name: /^Connectors:/ }));
    const github = screen.getByRole("checkbox", { name: "Include GitHub in this Work" });
    expect(github).toBeDisabled();
    expect(github).not.toBeChecked();
    const linear = screen.getByRole("checkbox", { name: "Include Linear in this Work" });
    expect(linear).toBeDisabled();
    expect(linear).not.toBeChecked();
    expect(screen.getAllByText("Not available on this Computer")).toHaveLength(2);
    expect(screen.getByRole("checkbox", { name: "Include Talgent in this Work" })).toBeChecked();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(start.calls).toHaveLength(1));
    expect(start.calls[0]).toMatchObject({ computerId: "mc-1" });
    expect((start.calls[0] as { creationCapabilities?: unknown }).creationCapabilities).toBeUndefined();
  });

  it("keeps offering the provided models on a Cloud", async () => {
    const { user, start } = await startOn([cloud, studio]);
    await user.click(await screen.findByRole("button", { name: "Model and thinking effort" }));
    expect(within(await screen.findByRole("listbox", { name: "Models" })).getAllByRole("option").map(option => option.textContent)).toEqual(["deepseek-v4-proDeepSeek"]);
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Start work" }));
    await waitFor(() => expect(start.calls).toEqual([expect.objectContaining({ computerId: "cloud-1", model: expect.objectContaining({ configurationId: "deepseek-pro", machineLogin: false }), thinkingLevel: "high" })]));
  });
});

describe("A Work's model", () => {
  it("shows the machine's Claude login on a Work that uses it", async () => {
    services.agent.getWork = (request: { workId: string }) => ({ work: { workId: request.workId, state: "working", allowedActions: ["stop"], version: 1n, shareReaderIds: [], startupProgress: { status: "completed" }, model: { machineLogin: true } } });
    services.agent.getThinkingLevel = () => ({ level: "max" });
    const user = userEvent.setup();
    await renderWorkbench("/works/pw-1");
    const trigger = await screen.findByRole("button", { name: "Model and thinking effort" });
    await waitFor(() => expect(trigger).toHaveTextContent("This machine's Claude loginMax"));
    await user.click(trigger);
    expect(within(await screen.findByRole("radiogroup", { name: "Thinking effort" })).getAllByRole("radio")).toHaveLength(5);
  });
});

describe("Personal Work", () => {
  it("lets its owner rename it and share it with anyone in the directory", async () => {
    const change = recorder((request: { title: string }) => ({ title: request.title }));
    const grant = recorder((request: { readerUserId: string }) => { shareReaderIds = [...shareReaderIds, request.readerUserId]; return {}; });
    Object.assign(services.personal, { changePersonalWorkTitle: change, grantPersonalWorkShare: grant, searchShareReaders: () => ({ users: [{ userId: "lin", username: "lin", displayName: "Lin Chen" }, { userId, username: "octocat", displayName: "Octo Cat" }] }) });
    const user = userEvent.setup();
    await renderWorkbench("/works/pw-1");
    await user.click(await screen.findByRole("button", { name: "Rename work" }));
    const rename = await screen.findByRole("dialog");
    await user.clear(within(rename).getByLabelText("Title"));
    await user.type(within(rename).getByLabelText("Title"), "Weekly report");
    await user.click(within(rename).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(change.calls).toEqual([expect.objectContaining({ workId: "pw-1", title: "Weekly report" })]));

    await user.click(screen.getByRole("button", { name: "Share" }));
    const share = await screen.findByRole("dialog", { name: "Share this work" });
    await user.type(within(share).getByLabelText("Find people"), "li");
    // The owner is never offered to themselves.
    const offered = await within(share).findByRole("option", { name: /Lin Chen/ });
    expect(within(share).queryByRole("option", { name: /Octo Cat/ })).not.toBeInTheDocument();
    await user.click(offered);
    await waitFor(() => expect(grant.calls).toEqual([expect.objectContaining({ workId: "pw-1", readerUserId: "lin" })]));
  });

  it("shows a shared Work read-only to a Work Share holder", async () => {
    Object.assign(services.personal, { listLibraryFiles: () => ({ files: [], works: [{ workId: "shared-1", title: "Clean the Q3 channel data", sharedByUserId: "lin" }] }) });
    await renderWorkbench("/works/shared-1");
    await screen.findByRole("heading", { name: "Clean the Q3 channel data" });
    expect(screen.queryByLabelText("Instruction")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Share" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Rename work" })).not.toBeInTheDocument();
  });

  it("tells someone holding no Work Share that the conversation is private", async () => {
    const watch = recorder(() => ({}));
    services.stream.watchStream = watch as never;
    await renderWorkbench("/works/shared-1");
    expect(await screen.findByText("This conversation is private")).toBeInTheDocument();
    expect(watch.calls, "no stream is opened without a Share").toHaveLength(0);
  });

  it("offers no title, Share or files on the person's own Work of a Project", async () => {
    // Agent lists files only for a Personal Work; a Work of a Project keeps
    // them in its Project's spaces.
    const files = recorder(() => { throw new ConnectError("only a Personal Work has this", Code.FailedPrecondition); });
    services.workFile.listWorkFiles = files as never;
    await renderWorkbench("/works/project-work");
    expect(await screen.findByLabelText("Instruction")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Share" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Rename work" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Attachments" })).not.toBeInTheDocument();
    expect(screen.queryByText("Files couldn’t be loaded.")).not.toBeInTheDocument();
    expect(files.calls).toHaveLength(0);
  });

  it("lists a shared Work's files only when it is a Personal Work", async () => {
    const files = recorder(() => ({ files: [{ workId: "shared-1", kind: WorkFileKind.ARTIFACT, name: "channels.csv", sizeBytes: 12n, contentType: "text/csv", shareLinks: [] }] }));
    services.workFile.listWorkFiles = files as never;
    let personal = true;
    services.personal.listLibraryFiles = () => ({ files: [], works: [{ workId: "shared-1", title: personal ? "Clean the Q3 channel data" : "", sharedByUserId: "lin", personal }] });
    await renderWorkbench("/works/shared-1");
    expect(await screen.findByRole("button", { name: "Preview channels.csv" })).toBeInTheDocument();
    cleanup();

    personal = false;
    files.calls.length = 0;
    await renderWorkbench("/works/shared-1");
    await screen.findByTestId("agent-stream-panel");
    expect(screen.queryByRole("button", { name: "Preview channels.csv" })).not.toBeInTheDocument();
    expect(files.calls).toHaveLength(0);
  });

  it("says when the Work no longer exists", async () => {
    services.agent.getWork = () => { throw new ConnectError("gone", Code.NotFound); };
    await renderWorkbench("/works/pw-gone");
    expect(await screen.findByText("This Work no longer exists")).toBeInTheDocument();
  });
});

describe("Disconnection", () => {
  const since = new Date(Date.now() - 60_000);
  const clock = since.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  const stops = new Date(since.getTime() + 5 * 60_000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  const note = `Disconnected since ${clock}. The Work stops at ${stops} unless it reconnects by then.`;

  it("qualifies a disconnected Work's state in Recent and its toolbar, and clears once observed", async () => {
    let disconnected = true;
    services.personal.listPersonalWorks = () => ({ works: works.map(work => work.workId === "pw-1" && disconnected ? { ...work, disconnectedSince: timestampFromDate(since) } : work) });
    services.agent.getWork = (request: { workId: string }) => ({ work: { workId: request.workId, state: "working", allowedActions: ["stop"], version: 1n, shareReaderIds: [], startupProgress: { status: "completed" }, disconnectedSince: disconnected ? timestampFromDate(since) : undefined } });
    const client = await renderWorkbench("/works/pw-1");
    const recent = await screen.findByRole("listbox", { name: "Recent" });
    const row = () => within(recent).getAllByRole("option")[0];
    await waitFor(() => expect(row()).toHaveTextContent("Draft this week's status report · Working · Disconnected"));
    // The status's accessible name says what the Disconnection means.
    await screen.findByRole("button", { name: `Work: Working · Disconnected. ${note}` });
    // Its tooltip, opened from the keyboard, explains the Disconnection. The
    // toolbar re-renders as the Work's state arrives, so the button is found anew.
    await userEvent.keyboard("{Escape}");
    await waitFor(() => {
      const status = screen.getByRole("button", { name: `Work: Working · Disconnected. ${note}` });
      if (document.activeElement !== status) act(() => status.focus());
      expect(screen.getByText(note)).toBeInTheDocument();
    }, { timeout: 3000 });

    disconnected = false;
    await act(async () => { await client.refetchQueries(); });
    await waitFor(() => expect(row()).not.toHaveTextContent("Disconnected"));
    expect(row()).toHaveTextContent("Draft this week's status report · Working");
    expect(await screen.findByRole("button", { name: "Work: Working" })).toBeInTheDocument();
  });

  // A disconnected Work's live Turn and requests are not observed:
  // the Work does not read them or report their absence as an error, and it
  // still takes the owner's instruction into its buffer.
  it("takes an instruction while disconnected without reading the live Turn or showing errors", async () => {
    const turn = recorder(() => ({ turnId: "", active: false }));
    const requests = recorder(() => ({ interactions: [] }));
    const send = recorder(() => ({}));
    Object.assign(services.agent, { getCurrentTurn: turn, listPendingInteractions: requests, sendMessage: send,
      getWork: (request: { workId: string }) => ({ work: { workId: request.workId, state: "working", allowedActions: ["stop"], version: 1n, shareReaderIds: [], startupProgress: { status: "completed" }, disconnectedSince: timestampFromDate(since) } }) });
    await renderWorkbench("/works/pw-1");
    await screen.findByRole("button", { name: `Work: Working · Disconnected. ${note}` });
    const input = await screen.findByLabelText("Instruction");
    await userEvent.type(input, "Keep going");
    const sendButton = screen.getByRole("button", { name: "Send instruction" });
    await waitFor(() => expect(sendButton).toBeEnabled());
    await userEvent.click(sendButton);
    await waitFor(() => expect(send.calls).toHaveLength(1));
    expect(send.calls[0]).toMatchObject({ workId: "pw-1", text: "Keep going" });
    expect(turn.calls).toHaveLength(0);
    expect(requests.calls).toHaveLength(0);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  // Before Agent records the Disconnection, or while Dispatcher reattaches,
  // the reads answer Unavailable; that is the same expected state.
  it("shows no error and keeps the composer usable while the live Turn is unavailable", async () => {
    const unavailable = () => { throw new ConnectError("not observed", Code.Unavailable); };
    const send = recorder(() => ({}));
    Object.assign(services.agent, { getCurrentTurn: unavailable, listPendingInteractions: unavailable, sendMessage: send });
    await renderWorkbench("/works/pw-1");
    const input = await screen.findByLabelText("Instruction");
    await userEvent.type(input, "Keep going");
    const sendButton = screen.getByRole("button", { name: "Send instruction" });
    await waitFor(() => expect(sendButton).toBeEnabled());
    expect(screen.queryByText("Current turn unavailable.")).not.toBeInTheDocument();
    expect(screen.queryByText("Could not load the current request.")).not.toBeInTheDocument();
    await userEvent.click(sendButton);
    await waitFor(() => expect(send.calls).toHaveLength(1));
  });

  it("says a Work stopped because it was disconnected, and how to continue", async () => {
    services.personal.listPersonalWorks = () => ({ works: works.map(work => work.workId === "pw-1" ? { ...work, state: "stopped", stateReason: "disconnected" } : work) });
    services.agent.getWork = (request: { workId: string }) => ({ work: { workId: request.workId, state: "stopped", stateReason: "disconnected", allowedActions: ["resume", "archive"], version: 1n, shareReaderIds: [], startupProgress: { status: "completed" } } });
    services.stream.watchStream = async function* (request: { agentWorkId?: string }, context: HandlerContext) {
      yield { payload: { case: "workState", value: { agentWorkId: request.agentWorkId, state: "stopped", subagents: [] } } };
      await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
    } as never;
    await renderWorkbench("/works/pw-1");
    expect(await screen.findByRole("button", { name: "Work: Stopped · Disconnected" })).toBeInTheDocument();
    const recent = await screen.findByRole("listbox", { name: "Recent" });
    await waitFor(() => expect(within(recent).getAllByRole("option")[0]).toHaveTextContent("Stopped · Disconnected"), { timeout: 3000 });
    expect(screen.getByText("Stopped after being disconnected for 5 minutes. Resume continues the conversation.")).toBeInTheDocument();
  });

  // A Resume refused because the Work's Managed Computer is away leaves the
  // Work Stopped with its message kept; the Resume is requested again when
  // the machine reconnects (Agent model: Retry on reconnection).
  it("keeps the conversation and says the Work resumes when its offline Computer reconnects", async () => {
    let owed = false;
    let reconnect = () => {};
    const reconnected = new Promise<void>(resolve => { reconnect = resolve; });
    const send = recorder(() => ({}));
    const resume = recorder(() => { owed = true; throw new ConnectError("agent: the Computer is unreachable", Code.FailedPrecondition); });
    services.personal.listPersonalWorks = () => ({ works: works.map(work => work.workId === "pw-1" ? { ...work, state: "stopped" } : work) });
    services.computer.listUsableComputers = () => ({ computers: [{ id: "studio-1", name: "Studio", kind: UsableComputerKind.MANAGED, reachable: false }] });
    Object.assign(services.agent, { sendMessage: send, resumeWork: resume,
      getWork: (request: { workId: string }) => ({ work: { workId: request.workId, state: "stopped", allowedActions: ["resume", "archive"], version: 1n, shareReaderIds: [], computerId: "studio-1", permissionMode: PermissionMode.BYPASS_PERMISSIONS, resumeOnReconnection: owed, startupProgress: { requestId: "first", status: "completed" } } }) });
    services.stream.watchStream = async function* (request: { agentWorkId?: string }, context: HandlerContext) {
      yield { payload: { case: "workState", value: { agentWorkId: request.agentWorkId, state: "stopped", subagents: [] } } };
      await reconnected;
      yield { payload: { case: "workState", value: { agentWorkId: request.agentWorkId, state: "running", subagents: [] } } };
      await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
    } as never;
    await renderWorkbench("/works/pw-1");
    const input = await screen.findByLabelText("Instruction");
    await userEvent.type(input, "Pick this up");
    const sendButton = screen.getByRole("button", { name: "Send instruction" });
    await waitFor(() => expect(sendButton).toBeEnabled());
    await userEvent.click(sendButton);
    await waitFor(() => expect(resume.calls).toHaveLength(1));
    expect(send.calls[0]).toMatchObject({ workId: "pw-1", text: "Pick this up" });
    expect(await screen.findByText("Studio is offline. Your messages are kept, and the Work resumes with them when it reconnects.")).toBeInTheDocument();
    expect(screen.getByLabelText("Instruction")).toBeInTheDocument();
    expect(screen.queryByText("Work couldn’t start")).not.toBeInTheDocument();
    expect(screen.queryByText(/unreachable|failed_precondition/)).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    // The machine is back and the owed Resume ran: the refusal describes a
    // Work that is no longer stopped.
    owed = false;
    reconnect();
    await waitFor(() => expect(screen.queryByText(/Your messages are kept/)).not.toBeInTheDocument());
    expect(screen.queryByText("This Work's computer is offline. Try again once it reconnects.")).not.toBeInTheDocument();
  });

  it("says why a Work failed, such as its Computer being removed", async () => {
    services.personal.listPersonalWorks = () => ({ works: works.map(work => work.workId === "pw-1" ? { ...work, state: "failed" } : work) });
    services.agent.getWork = (request: { workId: string }) => ({ work: { workId: request.workId, state: "failed", diagnosticReason: "The Work's Computer was removed.", allowedActions: ["archive", "restart"], version: 1n, shareReaderIds: [], startupProgress: { status: "completed" } } });
    services.stream.watchStream = async function* (request: { agentWorkId?: string }, context: HandlerContext) {
      yield { payload: { case: "workState", value: { agentWorkId: request.agentWorkId, state: "failed", subagents: [] } } };
      await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
    } as never;
    await renderWorkbench("/works/pw-1");
    expect(await screen.findByText("This Work failed. The Work's Computer was removed.")).toBeInTheDocument();
  });
});

describe("Library", () => {
  const library = {
    files: [
      { workId: "pw-1", workTitle: "Draft this week's status report", name: "status.md", sizeBytes: 6144n, contentType: "text/markdown", addedAt: minutesAgo(5) },
    ],
    works: [{ workId: "pw-1", title: "Draft this week's status report" }],
  };

  it("narrows the Library by name on the server", async () => {
    const list = recorder((request: { nameQuery: string }) => request.nameQuery ? { files: [], works: library.works } : library);
    services.personal.listLibraryFiles = list;
    const user = userEvent.setup();
    await renderWorkbench("/library");
    await within(await screen.findByRole("grid", { name: "Files" })).findByRole("row", { name: /status\.md/ });
    await user.type(screen.getByLabelText("Find a file"), "nothing");
    await waitFor(() => expect(list.calls.at(-1)).toMatchObject({ nameQuery: "nothing" }));
  });
});
