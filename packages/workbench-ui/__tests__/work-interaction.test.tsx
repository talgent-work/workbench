import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { create } from "@bufbuild/protobuf";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MessageDetailFormat, PendingInteractionSchema, UnifiedMessageKind, UnifiedMessageSchema } from "@talgent-work/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import { afterEach, expect, it, vi } from "vitest";
import { WorkInteraction } from "../components/work/work-interaction";

const api = vi.hoisted(() => ({ respond: vi.fn(), list: vi.fn(), detail: vi.fn() }));
vi.mock("../lib/client", () => ({ agentWorkClient: { respondToInteraction: api.respond }, streamClient: { listMessagesBySession: api.list, getMessageDetail: api.detail } }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

const questions = [
  { question: "First question?", options: [{ label: "A" }, { label: "B" }] },
  { question: "Second question?", options: [{ label: "C" }, { label: "D" }] },
  { question: "Third question?", options: [{ label: "E" }, { label: "F" }] },
];

it("skips remaining questions without losing answers or emitting a new user message", async () => {
  api.respond.mockResolvedValue({});
  const request = create(PendingInteractionSchema, { id: "question", toolName: "AskUserQuestion", questionInputJson: JSON.stringify({ questions }) });
  const responded = vi.fn();
  render(<WorkInteraction workId="work" request={request} onResponded={responded} />);
  await userEvent.click(screen.getByRole("radio", { name: /A/ }));
  await userEvent.click(screen.getByRole("button", { name: "Next" }));
  await userEvent.type(screen.getByRole("textbox", { name: "Write your own answer" }), "Original answer");
  await userEvent.click(screen.getByRole("button", { name: "Skip remaining questions" }));
  await waitFor(() => expect(api.respond).toHaveBeenCalledTimes(1));
  const payload = api.respond.mock.calls[0][0];
  expect(payload).toMatchObject({ workId: "work", interactionId: "question", allow: true, alwaysAllow: false });
  expect(JSON.parse(payload.updatedInputJson)).toEqual({ questions, answers: { "First question?": "A", "Second question?": "Original answer" } });
  expect(responded).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("status")).toBeInTheDocument();
});

it("submits all-skipped questions with an empty answers object", async () => {
  api.respond.mockResolvedValue({});
  render(<WorkInteraction workId="work" request={create(PendingInteractionSchema, { id: "skip", toolName: "AskUserQuestion", questionInputJson: JSON.stringify({ questions }) })} onResponded={vi.fn()} />);
  await userEvent.click(screen.getByRole("button", { name: "Skip remaining questions" }));
  await waitFor(() => expect(api.respond).toHaveBeenCalledTimes(1));
  expect(api.respond.mock.calls[0][0].allow).toBe(true);
  expect(JSON.parse(api.respond.mock.calls[0][0].updatedInputJson)).toEqual({ questions, answers: {} });
});

it("offers Always allow only when supported and preserves the request after failure", async () => {
  api.respond.mockRejectedValueOnce(new Error("Connection lost")).mockResolvedValueOnce({});
  const request = create(PendingInteractionSchema, { id: "approval", toolName: "Bash", prompt: "Allow original command?", canAlwaysAllow: true });
  const page = render(<WorkInteraction workId="work" request={request} onResponded={vi.fn()} />);
  await userEvent.click(screen.getByRole("button", { name: "Always allow" }));
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  expect(screen.getByText("Allow original command?")).toBeVisible();
  await userEvent.click(screen.getByRole("button", { name: "Always allow" }));
  expect(api.respond).toHaveBeenLastCalledWith({ workId: "work", interactionId: "approval", allow: true, alwaysAllow: true, updatedInputJson: "" });
  page.rerender(<WorkInteraction key="other" workId="work" request={create(PendingInteractionSchema, { id: "other", toolName: "Bash", prompt: "Allow?" })} onResponded={vi.fn()} />);
  expect(screen.queryByRole("button", { name: "Always allow" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Allow once/ })).toBeEnabled();
});

it("preserves edited multi-select answers through navigation and retries the same complete reply", async () => {
  const questions = [
    { question: "Platforms?", multiSelect: true, options: [{ label: "Web" }, { label: "Mobile" }] },
    { question: "Notes?" },
    { question: "Budget?", options: [{ label: "Fixed" }] },
  ];
  api.respond.mockRejectedValueOnce(new Error("Reply unavailable")).mockResolvedValueOnce({});
  const responded = vi.fn();
  render(<WorkInteraction workId="work" request={create(PendingInteractionSchema, { id: "edit-answers", toolName: "AskUserQuestion", questionInputJson: JSON.stringify({ questions }) })} onResponded={responded} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("checkbox", { name: /Web/ }));
  await user.click(screen.getByRole("checkbox", { name: /Mobile/ }));
  await user.click(screen.getByRole("checkbox", { name: /Web/ }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.type(screen.getByRole("textbox", { name: "Write your own answer" }), "保留原文");
  await user.click(screen.getByRole("button", { name: "Previous question" }));
  expect(screen.getByRole("checkbox", { name: /Mobile/ })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: /Web/ })).not.toBeChecked();
  await user.click(screen.getByRole("button", { name: "Next question" }));
  expect(screen.getByRole("textbox", { name: "Write your own answer" })).toHaveValue("保留原文");
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("radio", { name: /Fixed/ }));
  expect(api.respond).not.toHaveBeenCalled();
  // Skipping the final question omits even a previously selected answer.
  await user.click(screen.getByRole("button", { name: "Skip" }));
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  expect(responded).not.toHaveBeenCalled();
  expect(screen.getByRole("radio", { name: /Fixed/ })).toBeChecked();
  await user.click(screen.getByRole("button", { name: "Skip" }));
  expect(await screen.findByRole("status")).toBeInTheDocument();
  expect(api.respond).toHaveBeenCalledTimes(2);
  expect(api.respond.mock.calls[1][0]).toEqual(api.respond.mock.calls[0][0]);
  expect(JSON.parse(api.respond.mock.calls[1][0].updatedInputJson)).toEqual({ questions, answers: { "Platforms?": "Mobile", "Notes?": "保留原文" } });
  expect(responded).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Skip" })).toBeDisabled();
});

it("retries permission operation lookup and only fetches the original input when opened", async () => {
  const action = create(UnifiedMessageSchema, { id: "operation", agentWorkId: "work", kind: UnifiedMessageKind.ACTION, detailFormat: MessageDetailFormat.JSON, payload: { case: "action", value: { toolCallId: "command", toolName: "Bash", summary: "printf preview" } } });
  api.list.mockRejectedValueOnce(new Error("History unavailable")).mockResolvedValue({ messages: [action] });
  api.detail.mockResolvedValue({ format: MessageDetailFormat.JSON, text: '{"command":"printf preview"}', totalLength: 28, nextOffset: 28 });
  api.respond.mockResolvedValue({});
  const responded = vi.fn();
  render(<QueryClientProvider client={new QueryClient()}><WorkInteraction workId="work" request={create(PendingInteractionSchema, { id: "approval", toolName: "Bash", toolCallId: "command" })} onResponded={responded} /></QueryClientProvider>);
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByText("printf preview")).toBeVisible();
  expect(api.list).toHaveBeenLastCalledWith(expect.objectContaining({ agentWorkId: "work", toolCallIds: ["command"], kinds: [UnifiedMessageKind.ACTION] }), expect.anything());
  expect(api.detail).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "View full input" }));
  expect(await screen.findByText('{"command":"printf preview"}')).toBeVisible();
  expect(api.detail).toHaveBeenCalledWith(expect.objectContaining({ agentWorkId: "work", messageId: "operation" }), expect.anything());
  await userEvent.keyboard("{Escape}");
  expect(await screen.findByRole("status")).toBeInTheDocument();
  expect(api.respond).toHaveBeenCalledExactlyOnceWith({ workId: "work", interactionId: "approval", allow: false, alwaysAllow: false, updatedInputJson: "" });
  expect(responded).toHaveBeenCalledTimes(1);
});
