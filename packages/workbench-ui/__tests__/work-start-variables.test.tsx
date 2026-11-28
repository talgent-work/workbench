import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { WorkModelOptionSchema } from "@talgent/contracts/agent/public/v1/model_pb";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { WorkStartForm } from "../components/work-start/work-start-form";
import { useStartWork } from "../lib/standalone-work-service";
import type { WorkStartSource } from "../lib/work-start-source";
import { recorder, resetServices, services } from "../test-support/backend";

vi.mock("../lib/transport", async () => (await import("../test-support/backend")).transportModule);

const source: WorkStartSource = {
  draft: { key: "variables-test", instruction: "" },
  key: ["variables-test"],
  loadOptions: async () => ({ models: [create(WorkModelOptionSchema, { configurationId: "default", name: "Default" })], plugins: [], connectors: [] }),
  loadRepositories: async () => ({ repositories: [], recentRepositoryIds: [], repositoriesUnavailable: false }),
};

let startWork: ReturnType<typeof recorder<{ personalVariables?: { names: string[] } }, { workId: string }>>;

function keepVariables(names: string[]) {
  services.personalVariable.listPersonalVariables = () => ({ variables: names.map(name => ({ name })) });
}

// The form starts a Standalone Work the way New work does.
function StartForm() {
  const start = useStartWork();
  return <WorkStartForm source={source} onStart={async input => { await start.mutateAsync(input); }} />;
}

function renderForm() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter><StartForm /></MemoryRouter></QueryClientProvider>);
}

async function start(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByRole("textbox", { name: "Initial instruction" }), "Draft my plan");
  await user.click(screen.getByRole("button", { name: "Start work" }));
}

const sentNames = () => startWork.calls.at(-1)?.personalVariables?.names;

beforeEach(() => {
  resetServices();
  Object.assign(services.marketplace, { listPlugins: () => ({}), listInstalledPlugins: () => ({}), listConnectors: () => ({}), listProviders: () => ({}), getProviderConnectionState: () => ({}) });
  startWork = recorder(() => ({ workId: "w-1" }));
  services.work.startWork = startWork;
});
afterEach(cleanup);

it("enables every Personal Variable by default and starts the Work with all of them", async () => {
  const user = userEvent.setup();
  keepVariables(["ANTHROPIC_MODEL", "API_TOKEN"]);
  renderForm();

  await user.click(await screen.findByRole("button", { name: "Variables: 2 variables" }));
  const card = screen.getByRole("dialog", { name: "Variables for this Work" });
  const personal = within(card).getByRole("group", { name: "Personal" });
  expect(within(personal).getByRole("checkbox", { name: "Enable ANTHROPIC_MODEL for this Work" })).toBeChecked();
  expect(within(personal).getByRole("checkbox", { name: "Enable API_TOKEN for this Work" })).toBeChecked();
  expect(within(card).getByRole("link", { name: "Manage variables in Profile" })).toHaveAttribute("href", "/profile");
  expect(within(card).getByText("Applies to this Work only")).toBeInTheDocument();
  expect(within(card).queryByRole("searchbox")).not.toBeInTheDocument();
  await user.keyboard("{Escape}");

  await start(user);
  await vi.waitFor(() => expect(startWork).toHaveBeenCalledOnce());
  expect(sentNames()).toEqual(["ANTHROPIC_MODEL", "API_TOKEN"]);
});

it("strikes through a variable turned off, counts it and leaves it out of the Work", async () => {
  const user = userEvent.setup();
  keepVariables(["ANTHROPIC_MODEL", "API_TOKEN"]);
  renderForm();

  await user.click(await screen.findByRole("button", { name: "Variables: 2 variables" }));
  const off = screen.getByRole("checkbox", { name: "Enable API_TOKEN for this Work" });
  await user.click(off);
  expect(off).not.toBeChecked();
  expect(off.closest(".wst-check-row")).toHaveAttribute("data-struck", "true");
  await user.keyboard("{Escape}");
  expect(screen.getByRole("button", { name: "Variables: 1 of 2 variables" })).toBeInTheDocument();

  await start(user);
  await vi.waitFor(() => expect(startWork).toHaveBeenCalledOnce());
  expect(sentNames()).toEqual(["ANTHROPIC_MODEL"]);
});

it("searches the variables once more than six exist", async () => {
  const user = userEvent.setup();
  keepVariables(["A_ONE", "B_TWO", "C_THREE", "D_FOUR", "E_FIVE", "F_SIX", "G_TOKEN"]);
  renderForm();

  await user.click(await screen.findByRole("button", { name: "Variables: 7 variables" }));
  await user.type(screen.getByRole("searchbox", { name: "Search variables" }), "token");
  expect(screen.getAllByRole("checkbox").map(box => box.getAttribute("aria-label"))).toEqual(["Enable G_TOKEN for this Work"]);
  await user.clear(screen.getByRole("searchbox", { name: "Search variables" }));
  await user.type(screen.getByRole("searchbox", { name: "Search variables" }), "missing");
  expect(screen.getByRole("status")).toHaveTextContent("No variables match.");
});

it("points a person without variables to Profile and starts the Work with none", async () => {
  const user = userEvent.setup();
  keepVariables([]);
  renderForm();

  await user.click(await screen.findByRole("button", { name: "Variables: Variables" }));
  const card = screen.getByRole("dialog", { name: "Variables for this Work" });
  expect(within(card).getByText(/No variables yet/)).toBeInTheDocument();
  expect(within(card).getByRole("link", { name: "Manage variables in Profile" })).toHaveAttribute("href", "/profile");
  await user.keyboard("{Escape}");

  await start(user);
  await vi.waitFor(() => expect(startWork).toHaveBeenCalledOnce());
  expect(sentNames()).toEqual([]);
});

it("starts nothing while the variables cannot be loaded and recovers on retry", async () => {
  const user = userEvent.setup();
  services.personalVariable.listPersonalVariables = () => { throw new ConnectError("down", Code.Unavailable); };
  renderForm();

  const alert = await screen.findByText(/Variables could not be loaded/);
  await start(user);
  expect(startWork).not.toHaveBeenCalled();

  keepVariables(["API_TOKEN"]);
  await user.click(within(alert).getByRole("button", { name: "Retry" }));
  expect(await screen.findByRole("button", { name: "Variables: 1 variable" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Start work" }));
  await vi.waitFor(() => expect(startWork).toHaveBeenCalledOnce());
  expect(sentNames()).toEqual(["API_TOKEN"]);
});
