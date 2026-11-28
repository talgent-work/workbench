import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ModelVendor } from "@talgent/contracts/agent/public/v1/model_pb";
import { EntryShellContext, personalPathsAt } from "../components/shell/entry-shell";
import { ReadingContext } from "../components/shell/reading-context";
import { StandaloneWorkPage } from "../pages/workbench/standalone-work-page";
import { resetServices, services } from "../test-support/backend";

vi.mock("../lib/transport", async () => (await import("../test-support/backend")).transportModule);

const Layout = ({ children }: { children: ReactNode }) => <>{children}</>;
const reading = { content: null, open: () => undefined, close: () => undefined, contextToolbar: null, contextToggleToolbar: null, workStatusToolbar: null, toolbar: null };

// A Work whose startup failed, as Agent returns it to the person it represents.
function keepWork(replacedVariables: string[]) {
  services.agent.getWork = () => ({ work: {
    id: "w-1", state: "failed",
    startupProgress: { requestId: "w-1", admitted: true, status: "failed", reason: "The Agent couldn’t reach its model.", stages: [{ stage: "agent", status: "failed" }] },
    startupRequest: { requestId: "w-1", initialMessage: "Draft my plan", plugins: ["notes"], replacedVariables },
  } });
}

// A running Work on a platform Model Configuration, as Agent returns it to the person it represents.
function keepRunningWork(replacedVariables: string[]) {
  services.agent.getWork = () => ({ work: {
    id: "w-1", state: "working", allowedActions: ["stop"],
    startupProgress: { requestId: "w-1", admitted: true, status: "completed", stages: [{ stage: "agent", status: "completed" }] },
    startupRequest: { requestId: "w-1", initialMessage: "Draft my plan", initialThinkingLevel: "high", replacedVariables },
    model: { configurationId: "deepseek" },
    modelOption: { configurationId: "deepseek", name: "DeepSeek Flash", vendor: ModelVendor.DEEPSEEK, thinkingLevels: ["low", "high"], defaultThinkingLevel: "high" },
  } });
  services.agent.getThinkingLevel = () => ({ level: "high" });
}

function renderWork() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><EntryShellContext value={{ Layout, home: "/", personal: personalPathsAt("") }}><ReadingContext value={reading}>
    <MemoryRouter initialEntries={["/works/w-1"]}><Routes><Route path="/works/:workId" element={<StandaloneWorkPage />} /></Routes></MemoryRouter>
  </ReadingContext></EntryShellContext></QueryClientProvider>);
}

async function openModelSettings() {
  await userEvent.click(await screen.findByRole("button", { name: "Model and thinking effort" }));
  return screen.findByRole("dialog", { name: "Model settings" });
}

async function openRequest() {
  const panel = await screen.findByRole("region", { name: "Startup progress" });
  await userEvent.click(within(panel).getByText("View request"));
  return panel;
}

beforeEach(() => {
  resetServices();
  services.work.listWorks = () => ({ works: [] });
});
afterEach(cleanup);

it("names the platform variables the person's own variables replaced in the startup request", async () => {
  keepWork(["ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"]);
  renderWork();

  const panel = await openRequest();
  const term = within(panel).getByText("Your variables");
  expect(term.tagName).toBe("DT");
  expect(term.nextElementSibling).toHaveTextContent("Replaced the platform’s ANTHROPIC_BASE_URL, ANTHROPIC_MODEL");
});

it("says nothing of variables when none replaced a platform one", async () => {
  keepWork([]);
  renderWork();

  const panel = await openRequest();
  expect(within(panel).getByText("Plugins")).toBeInTheDocument();
  expect(within(panel).queryByText("Your variables")).not.toBeInTheDocument();
});

it("names the replaced platform variables beside a running Work's model, with the way to manage them", async () => {
  keepRunningWork(["ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"]);
  renderWork();

  const settings = await openModelSettings();
  expect(settings).toHaveTextContent("Your variables replace the platform’s ANTHROPIC_BASE_URL, ANTHROPIC_MODEL");
  expect(within(settings).getByText("ANTHROPIC_MODEL").closest("code")).not.toBeNull();
  expect(within(settings).getByRole("link", { name: "Manage variables in Profile" })).toHaveAttribute("href", "/profile");
});

it("adds nothing beside a running Work's model when none replaced a platform one", async () => {
  keepRunningWork([]);
  renderWork();

  const settings = await openModelSettings();
  expect(within(settings).getByText("DeepSeek Flash")).toBeInTheDocument();
  expect(settings).not.toHaveTextContent("Your variables");
  expect(within(settings).queryByRole("link", { name: "Manage variables in Profile" })).not.toBeInTheDocument();
});
