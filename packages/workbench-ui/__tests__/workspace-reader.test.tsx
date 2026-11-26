import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { WorkFilePreviewMode, WorkFilePreviewRefusal } from "@talgent/contracts/agent/public/v1/work_file_pb";
import { WorkWorkspaceEntryKind as Kind, WorkWorkspaceState as State } from "@talgent/contracts/agent/public/v1/work_workspace_pb";
import { ReadingContext, type ReadingContent } from "../components/shell/reading-context";
import { WorkspaceButton, WorkspaceReader } from "../components/workspace/workspace-reader";
import type { WorkspaceSource } from "../lib/workspace-reading";

const entry = (name: string, kind: Kind, attachmentName = "") => ({ name, kind, sizeBytes: 5n, attachmentName });
const listings: Record<string, ReturnType<typeof entry>[]> = {
  "": [entry("inputs", Kind.DIRECTORY), entry("notes.md", Kind.FILE), entry("latest", Kind.LINK)],
  inputs: [entry("brief.md", Kind.FILE, "brief.md")],
};

function source(state = State.AVAILABLE): WorkspaceSource & { [K in "browse" | "preview" | "download"]: ReturnType<typeof vi.fn> } {
  return {
    scope: `work:${state}`,
    browse: vi.fn(async (path: string) => ({ state, entries: listings[path] ?? [], total: (listings[path] ?? []).length })),
    // Refused, so the preview reads no bytes.
    preview: vi.fn(async () => ({ state, preview: { mode: WorkFilePreviewMode.REFUSED, display: 0, refusal: WorkFilePreviewRefusal.UNSUPPORTED, limitBytes: 0n, accessUrl: "", rendering: false } })),
    download: vi.fn(async () => ({ state, accessUrl: "/content?grant=download" })),
  };
}

function renderReader(reading: WorkspaceSource, close = vi.fn(), location: HTMLElement | null = null) {
  const tools = document.createElement("div");
  tools.setAttribute("data-testid", "tools");
  document.body.append(tools);
  const Attachment = ({ name }: { name: string }) => <p>Attachment {name}</p>;
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>
    <ReadingContext.Provider value={{ content: null, open: vi.fn(), close, contextToolbar: null, contextToggleToolbar: null, workStatusToolbar: null, toolbar: null }}>{children}</ReadingContext.Provider>
  </QueryClientProvider>;
  render(<WorkspaceReader source={reading} running={false} artifactsNote="Its Artifacts stay with the Work." Attachment={Attachment} location={location} tools={tools} />, { wrapper });
}

// The conversation beside the reading area already names the Work, so the
// top bar's path starts at the workspace.
it("names the open path from the workspace, not from what it belongs to", async () => {
  const location = document.createElement("div");
  document.body.append(location);
  renderReader(source(), vi.fn(), location);

  const path = await screen.findByRole("list", { name: "Workspace path" });
  expect(within(path).getAllByRole("listitem").map(item => item.textContent)).toEqual(["workspace"]);
  await userEvent.click(await screen.findByRole("button", { name: "notes.md" }));
  await waitFor(() => expect(within(path).getAllByRole("listitem").map(item => item.textContent)).toEqual(["workspace", "notes.md"]));
  location.remove();
});

it("browses folders, previews a file and downloads it through its source", async () => {
  const reading = source();
  const open = vi.spyOn(window, "open").mockReturnValue(null);
  renderReader(reading);

  expect(await screen.findByRole("button", { name: "latest, symbolic link, not followed" })).toHaveAttribute("aria-disabled", "true");
  await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
  await waitFor(() => expect(reading.preview).toHaveBeenCalledWith("notes.md", expect.anything()));
  await userEvent.click(screen.getByRole("button", { name: "Download file" }));
  await waitFor(() => expect(open).toHaveBeenCalledWith("/content?grant=download", "_blank", "noopener,noreferrer"));
  expect(reading.download).toHaveBeenCalledWith("notes.md");
  open.mockRestore();
});

it("opens an inputs/ entry as the Work's Attachment, which offers its own download", async () => {
  const reading = source();
  renderReader(reading);

  await userEvent.click(await screen.findByRole("button", { name: "inputs" }));
  await userEvent.click(await screen.findByRole("button", { name: "brief.md" }));
  expect(await screen.findByText("Attachment brief.md")).toBeInTheDocument();
  expect(reading.preview).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Download file" })).not.toBeInTheDocument();
});

it("says a closed Work's workspace is gone and points to its Artifacts", async () => {
  const close = vi.fn();
  renderReader(source(State.CLOSED), close);

  expect(await screen.findByText("The Work is closed, so its workspace is unavailable.")).toBeInTheDocument();
  expect(screen.getByText("Its Artifacts stay with the Work.")).toBeInTheDocument();
  expect(screen.queryByRole("tree")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Show Artifacts" }));
  expect(close).toHaveBeenCalled();
});

it("opens the workspace from the top bar and closes it on a second press", async () => {
  const content: ReadingContent = { kind: "work-workspace", label: "Draft report", workId: "work-1" };
  const open = vi.fn();
  const close = vi.fn();
  const value = (current: ReadingContent | null) => ({ content: current, open, close, contextToolbar: null, contextToggleToolbar: null, workStatusToolbar: null, toolbar: null });
  const { rerender } = render(<ReadingContext.Provider value={value(null)}><WorkspaceButton content={content} /></ReadingContext.Provider>);

  await userEvent.click(screen.getByRole("button", { name: "Workspace" }));
  expect(open).toHaveBeenCalledWith(content);
  rerender(<ReadingContext.Provider value={value(content)}><WorkspaceButton content={content} /></ReadingContext.Provider>);
  expect(screen.getByRole("button", { name: "Workspace" })).toHaveAttribute("aria-pressed", "true");
  await userEvent.click(screen.getByRole("button", { name: "Workspace" }));
  expect(close).toHaveBeenCalled();
});
