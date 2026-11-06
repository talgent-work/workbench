import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { ContentBlockSchema, ContentBlockType, ActionPayloadSchema, InteractionPayloadSchema, InteractionPhase, PermissionPayloadSchema, MessageDetailFormat, ObservationPayloadSchema, PermissionPhase, UnifiedMessageKind, UnifiedMessageSchema } from "@talgent-work/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import { afterEach, expect, it, vi } from "vitest";
import { WorkMessageFlow } from "../components/work/work-message-flow";
import { useWorkConversation } from "../hooks/use-work-conversation";
import { streamFeed, textMessage as message } from "./support/work-message-stream";

const api = vi.hoisted(() => ({ list: vi.fn(), watch: vi.fn(), detail: vi.fn(), toolDetail: vi.fn() }));
vi.mock("../lib/client", () => ({ streamClient: { listMessagesBySession: api.list, watchStream: api.watch, getMessageDetail: api.detail, getToolDetail: api.toolDetail } }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

const feed = () => streamFeed(api.watch);

const openTool = vi.fn();
function Page() {
  const conversation = useWorkConversation("work");
  return <WorkMessageFlow conversation={conversation} currentTurnId="second" waiting={false} onOpenTool={openTool} />;
}


// Tool input and output are read only once their call is opened.
it("loads a tool call's input and output only when it is opened", async () => {
  const messages = [
    message("01", "second", UnifiedMessageKind.USER, "Second request"),
    message("02", "second", UnifiedMessageKind.ASSISTANT, "Second process"),
    create(UnifiedMessageSchema, { ...message("03", "second", UnifiedMessageKind.ACTION, ""), detailFormat: MessageDetailFormat.JSON, payload: { case: "action", value: create(ActionPayloadSchema, { toolCallId: "call", toolName: "Bash", summary: "pwd" }) } }),
    create(UnifiedMessageSchema, { ...message("04", "second", UnifiedMessageKind.OBSERVATION, ""), detailFormat: MessageDetailFormat.TEXT, payload: { case: "observation", value: create(ObservationPayloadSchema, { toolCallId: "call" }) } }),
  ];
  api.list.mockImplementation(({ kinds }) => Promise.resolve({ messages: kinds?.length ? messages.filter(message => kinds.includes(message.kind)) : messages, hasMore: false }));
  feed();
  api.toolDetail.mockResolvedValue({ input: { format: MessageDetailFormat.JSON, text: '{"command":"pwd"}', totalLength: 17, nextOffset: 17 }, output: { format: MessageDetailFormat.TEXT, text: "/original/path\n", totalLength: 15, nextOffset: 15 } });
  render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  await userEvent.click(await screen.findByRole("button", { name: "Thinking…" }));
  expect(api.toolDetail).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Bash: pwd" }));
  expect(await screen.findByText("/original/path")).toBeInTheDocument();
  expect(api.toolDetail).toHaveBeenCalledTimes(1);
  expect(api.toolDetail).toHaveBeenCalledWith(expect.objectContaining({ toolCallId: "call" }), expect.anything());
  expect(screen.getByText('{"command":"pwd"}')).toBeVisible();
  expect(api.detail).not.toHaveBeenCalled();
});

it("keeps one collapsed question record with original answers and skips, without another user turn or permission summary", async () => {
  const action = create(UnifiedMessageSchema, { ...message("02", "second", UnifiedMessageKind.ACTION, ""), detailFormat: MessageDetailFormat.JSON, payload: { case: "action", value: create(ActionPayloadSchema, { toolCallId: "ask", toolName: "AskUserQuestion" }) } });
  const messages = [
    message("01", "second", UnifiedMessageKind.USER, "Original request"), action,
    create(UnifiedMessageSchema, { ...message("03", "second", UnifiedMessageKind.INTERACTION, ""), payload: { case: "interaction", value: create(InteractionPayloadSchema, { interactionId: "question", toolCallId: "ask", questionCount: 3, phase: InteractionPhase.REQUESTED }) } }),
    create(UnifiedMessageSchema, { ...message("04", "second", UnifiedMessageKind.INTERACTION, ""), payload: { case: "interaction", value: create(InteractionPayloadSchema, { interactionId: "question", toolCallId: "ask", questionCount: 3, phase: InteractionPhase.RESPONDED, responseJson: '{"Audience?":"Team", "Notes?":"保持原文\\nNo rewrite"}' }) } }),
    create(UnifiedMessageSchema, { ...message("05", "second", UnifiedMessageKind.OBSERVATION, ""), detailFormat: MessageDetailFormat.TEXT, payload: { case: "observation", value: create(ObservationPayloadSchema, { toolCallId: "ask" }) } }),
    create(UnifiedMessageSchema, { ...message("06", "second", UnifiedMessageKind.PERMISSION, ""), payload: { case: "permission", value: create(PermissionPayloadSchema, { permissionId: "permission", phase: PermissionPhase.DECIDED }) } }),
  ];
  api.list.mockResolvedValue({ messages, hasMore: false });
  feed();
  const input = JSON.stringify({ questions: [{ question: "Audience?" }, { question: "Notes?" }, { question: "Budget?" }] });
  api.detail.mockImplementation(({ offset }) => Promise.resolve({ format: MessageDetailFormat.JSON, text: offset ? input.slice(40) : input.slice(0, 40), totalLength: input.length, nextOffset: offset ? input.length : 40 }));
  render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  const record = await screen.findByRole("button", { name: "Asked 3 questions · Answered 2, skipped 1" });
  expect(api.detail).not.toHaveBeenCalled();
  expect(screen.queryByText(/Permission denied|Permission allowed/)).not.toBeInTheDocument();
  expect(within(screen.getByRole("navigation", { name: "Conversation turns" })).getAllByRole("button")).toHaveLength(1);
  await userEvent.click(record);
  expect(await screen.findByText("Audience?")).toBeVisible();
  expect(screen.getByText("Team")).toBeVisible();
  expect(screen.getByText("保持原文 No rewrite").textContent).toBe("保持原文\nNo rewrite");
  expect(screen.getByText("Budget?")).toBeVisible();
  expect(screen.getByText("Skipped")).toBeVisible();
  expect(api.detail).toHaveBeenCalledTimes(2);
  expect(api.detail).toHaveBeenLastCalledWith(expect.objectContaining({ messageId: "02", offset: 40 }), expect.anything());
});

it("recovers questions outside the history window and keeps a tool failure separate from the submitted answer", async () => {
  const action = create(UnifiedMessageSchema, { ...message("01", "second", UnifiedMessageKind.ACTION, ""), detailFormat: MessageDetailFormat.JSON, payload: { case: "action", value: create(ActionPayloadSchema, { toolCallId: "ask", toolName: "AskUserQuestion" }) } });
  const messages = [
    message("02", "second", UnifiedMessageKind.USER, "Review this choice"),
    create(UnifiedMessageSchema, { ...message("03", "second", UnifiedMessageKind.INTERACTION, ""), payload: { case: "interaction", value: create(InteractionPayloadSchema, { interactionId: "question", toolCallId: "ask", questionCount: 1, phase: InteractionPhase.RESPONDED, responseJson: '{"Audience?":"Team"}' }) } }),
    create(UnifiedMessageSchema, { ...message("04", "second", UnifiedMessageKind.OBSERVATION, ""), detailFormat: MessageDetailFormat.TEXT, payload: { case: "observation", value: create(ObservationPayloadSchema, { toolCallId: "ask", isError: true }) } }),
  ];
  api.list.mockImplementation(({ toolCallIds }) => Promise.resolve({ messages: toolCallIds?.length ? [action] : messages, hasMore: false }));
  feed();
  api.detail.mockRejectedValueOnce(new Error("Detail unavailable")).mockImplementation(({ messageId }) => {
    const text = messageId === "01" ? '{"questions":[{"question":"Audience?"}]}' : "The tool could not continue after the answer.";
    return Promise.resolve({ format: MessageDetailFormat.TEXT, text, totalLength: text.length, nextOffset: text.length });
  });
  render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  const record = await screen.findByRole("button", { name: "Asked 1 question · Answered 1, skipped 0" });
  expect(screen.queryByText("Tool failed")).not.toBeInTheDocument();
  expect(api.detail).not.toHaveBeenCalled();
  await userEvent.click(record);
  expect(await screen.findByRole("alert")).toHaveTextContent("Could not load original questions");
  expect(screen.queryByText("Skipped")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByText("Audience?")).toBeVisible();
  expect(screen.getByText("Team")).toBeVisible();
  expect(api.list).toHaveBeenCalledWith(expect.objectContaining({ agentWorkId: "work", agent: "", toolCallIds: ["ask"], kinds: [UnifiedMessageKind.ACTION], limit: 1 }), expect.anything());
  await userEvent.click(screen.getByRole("button", { name: "Show tool error" }));
  expect(await screen.findByText("The tool could not continue after the answer.")).toBeVisible();
  expect(api.detail).toHaveBeenLastCalledWith(expect.objectContaining({ messageId: "04" }), expect.anything());
  expect(screen.queryByText("Team")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Show questions" }));
  expect(await screen.findByText("Team")).toBeVisible();
  expect(screen.queryByText("The tool could not continue after the answer.")).not.toBeInTheDocument();
});

it("renders user images without displaying their encoded data as prose or TOC text", async () => {
  const image = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/N3sAAAAASUVORK5CYII=";
  const request = message("01", "first", UnifiedMessageKind.USER, "Review this image");
  if (request.payload.case === "message") request.payload.value.content.push(create(ContentBlockSchema, { type: ContentBlockType.IMAGE, mimeType: "image/png", text: image }));
  api.list.mockResolvedValue({ messages: [request, message("02", "second", UnifiedMessageKind.USER, "Continue")], hasMore: false });
  feed();
  render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  expect(await screen.findByRole("img", { name: "Attached image" })).toHaveAttribute("src", `data:image/png;base64,${image}`);
  expect(screen.queryByText(image, { exact: false })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Turn 1: Review this image" })).toBeVisible();
});

it("shows a Harness error without a known turn and fetches diagnostic details only on demand", async () => {
  const error = create(UnifiedMessageSchema, { id: "error", agentWorkId: "work", agent: "", kind: UnifiedMessageKind.ERROR, detailFormat: MessageDetailFormat.TEXT, payload: { case: "error", value: { code: "api_timeout" } } });
  api.list.mockResolvedValue({ messages: [error], hasMore: false });
  api.detail.mockResolvedValue({ format: MessageDetailFormat.TEXT, text: "Original provider timeout diagnostic", totalLength: 36, nextOffset: 36 });
  feed();
  render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  expect(await screen.findByText("api_timeout")).toBeVisible();
  expect(api.detail).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Error details" }));
  expect(await screen.findByText("Original provider timeout diagnostic")).toBeVisible();
  expect(api.detail).toHaveBeenCalledWith(expect.objectContaining({ agentWorkId: "work", messageId: "error" }), expect.anything());
});

it("keeps an open tool input visible when its output arrives", async () => {
  const input = create(UnifiedMessageSchema, { ...message("02", "second", UnifiedMessageKind.ACTION, ""), detailFormat: MessageDetailFormat.JSON, payload: { case: "action", value: create(ActionPayloadSchema, { toolCallId: "running", toolName: "Bash", summary: "long command" }) } });
  const output = create(UnifiedMessageSchema, { ...message("03", "second", UnifiedMessageKind.OBSERVATION, ""), detailFormat: MessageDetailFormat.TEXT, payload: { case: "observation", value: create(ObservationPayloadSchema, { toolCallId: "running" }) } });
  const messages = [message("01", "second", UnifiedMessageKind.USER, "Run command"), input];
  api.list.mockImplementation(({ kinds }) => Promise.resolve({ messages: kinds?.length ? messages.filter(message => kinds.includes(message.kind)) : messages, hasMore: false }));
  api.toolDetail.mockResolvedValue({ input: { format: MessageDetailFormat.JSON, text: '{"command":"original input"}', totalLength: 28, nextOffset: 28 } });
  const deliver = feed();
  render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  await userEvent.click(await screen.findByRole("button", { name: "Thinking…" }));
  await userEvent.click(screen.getByRole("button", { name: "Bash: long command" }));
  expect(await screen.findByText('{"command":"original input"}')).toBeVisible();
  messages.push(output);
  act(() => deliver(output));
  await waitFor(() => expect(screen.getByRole("button", { name: "Bash: long command" })).toHaveAttribute("aria-expanded", "true"));
  expect(screen.getByText('{"command":"original input"}')).toBeVisible();
});

function geometry(container: HTMLElement) {
  const scroller = container.querySelector<HTMLElement>(".wm-scroll")!;
  Object.defineProperties(scroller, {
    scrollHeight: { configurable: true, value: 1000 },
    clientHeight: { configurable: true, value: 400 },
  });
  const scrollTo = (top: number) => { scroller.scrollTop = top; fireEvent.scroll(scroller); };
  return { scroller, scrollTo };
}

it("lets a reader leave the latest messages by scrolling upward, even close to the end", async () => {
  const deliver = feed();
  api.list.mockResolvedValue({ messages: [message("01", "first", UnifiedMessageKind.USER, "Start"), message("02", "first", UnifiedMessageKind.ASSISTANT, "Working")], hasMore: false });
  const rendered = render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  expect(await screen.findByText("Working")).toBeVisible();
  const { scroller, scrollTo } = geometry(rendered.container);
  scrollTo(600);
  fireEvent.wheel(scroller, { deltaY: -40 });
  scrollTo(590);
  act(() => deliver(message("03", "first", UnifiedMessageKind.ASSISTANT, "Arrived while reading")));
  expect(await screen.findByText("Arrived while reading")).toBeVisible();
  expect(scroller.scrollTop).toBe(590);
  // Back at the very end, arriving messages are followed again.
  scrollTo(600);
  act(() => deliver(message("04", "first", UnifiedMessageKind.ASSISTANT, "Followed")));
  expect(await screen.findByText("Followed")).toBeVisible();
  await waitFor(() => expect(scroller.scrollTop).toBe(1000));
});

it("offers the way back to the latest messages once the reader is away from them, without reloading", async () => {
  feed();
  api.list.mockResolvedValue({ messages: [message("01", "first", UnifiedMessageKind.USER, "Start"), message("02", "first", UnifiedMessageKind.ASSISTANT, "Working")], hasMore: false });
  const rendered = render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  expect(await screen.findByText("Working")).toBeVisible();
  const { scroller, scrollTo } = geometry(rendered.container);
  scrollTo(600);
  expect(screen.queryByRole("button", { name: "Back to latest" })).not.toBeInTheDocument();
  fireEvent.wheel(scroller, { deltaY: -300 });
  scrollTo(300);
  const latest = await screen.findByRole("button", { name: "Back to latest" });
  const requests = api.list.mock.calls.length;
  await userEvent.click(latest);
  expect(scroller.scrollTop).toBe(1000);
  await waitFor(() => expect(screen.queryByRole("button", { name: "Back to latest" })).not.toBeInTheDocument());
  expect(api.list).toHaveBeenCalledTimes(requests);
});
