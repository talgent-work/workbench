import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { WorkAgentGuidance } from "../components/profile/work-agent-guidance";
import { MAX_WORKSPACE_INSTRUCTIONS } from "../lib/workspace-instructions-service";

const userId = "01a11fb2-e6c2-701f-80b7-38c54aa2eae9";
const agentId = "0193a7c2-6f1e-7c2a-9b3d-00000000a9e2";
const saved = { soul: "", instructions: "" };
const calls: Array<{ method: string; request: unknown }> = [];
// Makes reading the Workspace Instructions fail, as an unavailable server would.
let instructionsUnavailable = false;

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: userId } } }) }));
vi.mock("../lib/notifications", () => ({ notify: vi.fn() }));
vi.mock("../lib/client", () => ({
  agentWorkClient: {
    getMyWorkAgent: async () => ({ agentId, appearance: { name: "Ada", icon: { glyph: "bot", color: "green" } } }),
    ensureMyWorkAgent: async () => ({ agentId }),
    getAgentSoul: async () => ({ soul: saved.soul }),
    changeAgentSoul: async (request: { agentId: string; soul: string }) => {
      calls.push({ method: "changeAgentSoul", request });
      saved.soul = request.soul;
      return { soul: saved.soul };
    },
    getAgentWorkspaceInstructions: async () => {
      if (instructionsUnavailable) throw new Error("unavailable");
      return { instructions: saved.instructions };
    },
    changeAgentWorkspaceInstructions: async (request: { agentId: string; instructions: string }) => {
      calls.push({ method: "changeAgentWorkspaceInstructions", request });
      saved.instructions = request.instructions;
      return { instructions: saved.instructions };
    },
  },
}));

beforeEach(() => {
  saved.soul = "Answer with the conclusion first.";
  saved.instructions = "";
  calls.length = 0;
  instructionsUnavailable = false;
});

function renderGuidance() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  return render(<WorkAgentGuidance />, { wrapper });
}

it("edits the Soul and the workspace instructions in one place", async () => {
  const user = userEvent.setup();
  renderGuidance();

  const soul = await screen.findByRole("form", { name: "Soul" });
  expect(within(soul).getByRole("textbox", { name: "Soul" })).toHaveValue("Answer with the conclusion first.");

  await user.click(screen.getByRole("tab", { name: "Workspace instructions" }));
  const instructions = await screen.findByRole("form", { name: "Workspace instructions" });
  await user.type(within(instructions).getByRole("textbox", { name: "Workspace instructions" }), "  Run make check.  ");
  expect(within(instructions).getByText(`15 / ${MAX_WORKSPACE_INSTRUCTIONS.toLocaleString("en-US")}`)).toBeInTheDocument();

  // Switching away keeps the draft, and its tab says it is unsaved.
  await user.click(screen.getByRole("tab", { name: "Soul" }));
  expect(screen.getByRole("tab", { name: "Workspace instructions (unsaved)" })).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "Workspace instructions (unsaved)" }));
  const again = await screen.findByRole("form", { name: "Workspace instructions" });
  expect(within(again).getByRole("textbox", { name: "Workspace instructions" })).toHaveValue("  Run make check.  ");

  await user.click(within(again).getByRole("button", { name: "Save" }));
  await waitFor(() => expect(calls).toContainEqual({ method: "changeAgentWorkspaceInstructions", request: { agentId, instructions: "Run make check." } }));
  await waitFor(() => expect(screen.getByRole("tab", { name: "Workspace instructions" })).toBeInTheDocument());
  expect(calls.some(call => call.method === "changeAgentSoul")).toBe(false);
});

it("discards a draft back to what is saved", async () => {
  const user = userEvent.setup();
  renderGuidance();

  const soul = await screen.findByRole("form", { name: "Soul" });
  const box = within(soul).getByRole("textbox", { name: "Soul" });
  await user.type(box, " Always.");
  expect(screen.getByRole("tab", { name: "Soul (unsaved)" })).toBeInTheDocument();
  await user.click(within(soul).getByRole("button", { name: "Discard" }));
  expect(box).toHaveValue("Answer with the conclusion first.");
  expect(screen.getByRole("tab", { name: "Soul" })).toBeInTheDocument();
  expect(calls).toEqual([]);
});

it("refuses workspace instructions over the bound before saving", async () => {
  const user = userEvent.setup();
  renderGuidance();

  await user.click(await screen.findByRole("tab", { name: "Workspace instructions" }));
  const instructions = await screen.findByRole("form", { name: "Workspace instructions" });
  const box = within(instructions).getByRole("textbox", { name: "Workspace instructions" });
  await user.click(box);
  await user.paste("a".repeat(MAX_WORKSPACE_INSTRUCTIONS + 1));
  expect(within(instructions).getByText(`Use at most ${MAX_WORKSPACE_INSTRUCTIONS.toLocaleString("en-US")} characters.`)).toBeInTheDocument();
  expect(within(instructions).getByRole("button", { name: "Save" })).toBeDisabled();
});

it("offers a retry when the workspace instructions cannot be read, and keeps the Soul usable", async () => {
  instructionsUnavailable = true;
  const user = userEvent.setup();
  renderGuidance();

  await screen.findByRole("form", { name: "Soul" });
  await user.click(screen.getByRole("tab", { name: "Workspace instructions" }));
  expect(await screen.findByText("Your Work Agent's workspace instructions could not be loaded.")).toBeInTheDocument();
  instructionsUnavailable = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByRole("form", { name: "Workspace instructions" })).toBeInTheDocument();
});
