import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MessageDetailFormat } from "@talgent/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import { afterEach, expect, it, vi } from "vitest";
import { ToolMessageDetail } from "../components/work/tool-message-detail";
import { ToolReadingContent } from "../components/work/tool-reading-content";
import { ToolPreviewReading } from "../components/work/tool-preview-reading";

const api = vi.hoisted(() => ({ detail: vi.fn(), list: vi.fn(), toolDetail: vi.fn() }));
vi.mock("../lib/client", () => ({ streamClient: { getMessageDetail: api.detail, listMessagesBySession: api.list, getToolDetail: api.toolDetail } }));
afterEach(() => { cleanup(); api.detail.mockReset(); api.list.mockReset(); api.toolDetail.mockReset(); });

it("reads original detail in bounded pages and releases it when closed", async () => {
  const original = '{"text":"原文\\n保留", "spaces":  true}';
  api.detail.mockImplementation(({ offset }) => Promise.resolve({ format: MessageDetailFormat.JSON, text: offset ? "last page" : original, totalLength: 20000, nextOffset: offset ? 20000 : 16384 }));
  const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={cache}><ToolMessageDetail workId="work" messageId="output" /></QueryClientProvider>);
  await waitFor(() => expect(view.container.querySelector("pre")?.textContent).toBe(original));
  expect(api.detail).toHaveBeenCalledWith({ agentWorkId: "work", messageId: "output", offset: 0, limit: 16384 }, expect.objectContaining({ signal: expect.any(AbortSignal) }));
  await userEvent.click(screen.getByRole("button", { name: "Next page" }));
  expect(await screen.findByText("last page")).toBeInTheDocument();
  expect(view.container.querySelector("pre")?.textContent).toBe("last page");
  expect(api.detail).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 16384 }), expect.anything());
  await userEvent.click(screen.getByRole("button", { name: "Previous page" }));
  await waitFor(() => expect(view.container.querySelector("pre")?.textContent).toBe(original));
  view.unmount();
  await waitFor(() => expect(cache.getQueryCache().getAll()).toHaveLength(0));
});

it("distinguishes an empty tool result from an unavailable detail and allows retry", async () => {
  api.detail.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce({ format: MessageDetailFormat.TEXT, text: "", totalLength: 0, nextOffset: 0 });
  render(<QueryClientProvider client={new QueryClient()}><ToolMessageDetail workId="work" messageId="empty" /></QueryClientProvider>);
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByText("Empty output")).toBeInTheDocument();
  expect(screen.queryByText("No detail available")).not.toBeInTheDocument();
});

it("loads input and output in one request and releases both when closed", async () => {
  api.toolDetail.mockResolvedValue({ input: { format: MessageDetailFormat.JSON, text: '{"file_path":"original.md"}', totalLength: 27, nextOffset: 27 }, output: { format: MessageDetailFormat.TEXT, text: "Recorded file content", totalLength: 21, nextOffset: 21 } });
  const client = new QueryClient();
  const rendered = render(<QueryClientProvider client={client}><ToolReadingContent tool={{ workId: "work", toolCallId: "call", title: "Read: original.md" }} /></QueryClientProvider>);
  expect(await screen.findByText("Recorded file content")).toBeVisible();
  expect(screen.getByText('{"file_path":"original.md"}')).toBeVisible();
  expect(screen.queryByRole("button", { name: "Input" })).not.toBeInTheDocument();
  expect(api.toolDetail).toHaveBeenCalledExactlyOnceWith({ agentWorkId: "work", toolCallId: "call", inputOffset: 0, outputOffset: 0, limit: 16384 }, expect.objectContaining({ signal: expect.any(AbortSignal) }));
  expect(api.list).not.toHaveBeenCalled();
  expect(api.detail).not.toHaveBeenCalled();
  rendered.unmount();
  await waitFor(() => expect(client.getQueryCache().getAll()).toHaveLength(0));
});

it("distinguishes failed lookup, pending output and unavailable input", async () => {
  api.toolDetail.mockRejectedValueOnce(new Error("History offline")).mockResolvedValue({});
  render(<QueryClientProvider client={new QueryClient()}><ToolReadingContent tool={{ workId: "work", toolCallId: "call", title: "Bash: pending" }} /></QueryClientProvider>);
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  expect(screen.queryByText("No output yet")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByText("No output yet")).toBeVisible();
  expect(screen.getByText("Input unavailable")).toBeVisible();
  expect(screen.queryByText("Empty output")).not.toBeInTheDocument();
});

function openTool(title: string) {
  return render(<QueryClientProvider client={new QueryClient()}><ToolPreviewReading tool={{ workId: "work", toolCallId: "call", title }} toolbar={null} /></QueryClientProvider>);
}
const statusOutput = { format: MessageDetailFormat.TEXT, text: "File created successfully at: /workspace/report.md", totalLength: 50, nextOffset: 50 };
const writeInput = { format: MessageDetailFormat.JSON, text: '{"content":"INPUT BODY"}', totalLength: 24, nextOffset: 24 };

it("renders a whole-file Read by its type", async () => {
  api.toolDetail.mockResolvedValue({ input: writeInput, output: statusOutput, preview: { toolName: "Read", filePath: "/workspace/notes.md", content: "# Notes\n\nBody", startLine: 1, numLines: 3, totalLines: 3, hunks: [] } });
  openTool("Read: notes.md");
  expect(await screen.findByRole("heading", { name: "Notes" })).toBeVisible();
  expect(api.toolDetail).toHaveBeenCalledExactlyOnceWith({ agentWorkId: "work", toolCallId: "call", inputOffset: 0, outputOffset: 0, limit: 1 }, expect.objectContaining({ signal: expect.any(AbortSignal) }));
});

it("waits for a running call's result and retries a failed preview", async () => {
  api.toolDetail.mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ input: writeInput });
  openTool("Write: report.md");
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Retry preview" }));
  expect(await screen.findByText("Waiting for this call to finish…")).toBeVisible();
});
