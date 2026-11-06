import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { UnifiedMessageKind, UnifiedMessageSchema, type UnifiedMessage } from "@talgent-work/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import { afterEach, expect, it, vi } from "vitest";
import { WorkMessageFlow } from "../components/work/work-message-flow";
import { useWorkConversation } from "../hooks/use-work-conversation";
import { streamFeed, textMessage as message } from "./support/work-message-stream";

// A Turn's segments: each Steer starts one; the Harness's own notes never do.
const api = vi.hoisted(() => ({ list: vi.fn(), watch: vi.fn() }));
vi.mock("../lib/client", () => ({ streamClient: { listMessagesBySession: api.list, watchStream: api.watch } }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

const feed = () => streamFeed(api.watch);

function Page() {
  const conversation = useWorkConversation("work");
  return <WorkMessageFlow conversation={conversation} currentTurnId="second" waiting={false} onOpenTool={vi.fn()} />;
}

// The marker an interrupted tool use leaves is the Harness's own: it shows in
// the work, does not split the turn and is not taken for the person's input.
it("keeps a Harness note in the work instead of showing it as the person's update", async () => {
  const at = (seconds: number, value: UnifiedMessage) => create(UnifiedMessageSchema, { ...value, timestamp: timestampFromDate(new Date(1700000000000 + seconds * 1000)) });
  const messages = [
    at(0, message("31", "second", UnifiedMessageKind.USER, "Plan the release")),
    at(2, message("32", "second", UnifiedMessageKind.ASSISTANT, "Asking which date suits")),
    at(9, message("33", "second", UnifiedMessageKind.HARNESS_NOTE, "[Request interrupted by user for tool use]")),
  ];
  api.list.mockImplementation(({ kinds }) => Promise.resolve({ messages: kinds?.length ? messages.filter(message => kinds.includes(message.kind)) : messages, hasMore: false }));
  feed();
  render(<QueryClientProvider client={new QueryClient()}><Page /></QueryClientProvider>);
  expect(await screen.findByText("[Request interrupted by user for tool use]")).toBeVisible();
  // Regression (64333590c): the note was taken for a Steer and split the turn.
  expect(screen.getByText("Asking which date suits")).toBeVisible();
  expect(screen.queryByRole("button", { name: /before your update/ })).not.toBeInTheDocument();
});
