import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { ContentBlockType, UnifiedMessageSchema, type UnifiedMessage, type UnifiedMessageKind } from "@talgent/contracts/dispatcher/public/v1/unified_message_pb";
import type { Mock } from "vitest";

// A Work's live message stream for conversation tests: the watch mock yields
// each message delivered, as live or replayed, until the reader leaves.
export function streamFeed(watch: Mock) {
  let deliver: ((message: UnifiedMessage, replay?: boolean) => void) | undefined;
  watch.mockImplementation(async function* (_request: unknown, { signal }: { signal: AbortSignal }) {
    while (!signal.aborted) {
      let abort: () => void = () => {};
      const event = await new Promise<{ message: UnifiedMessage; replay: boolean } | undefined>(resolve => {
        deliver = (message, replay = false) => resolve({ message, replay });
        abort = () => resolve(undefined);
        signal.addEventListener("abort", abort, { once: true });
      });
      signal.removeEventListener("abort", abort);
      if (!event) return;
      yield { replay: event.replay, payload: { case: "message", value: event.message } };
    }
  });
  return (message: UnifiedMessage, replay = false) => deliver!(message, replay);
}

/** A root message of the Work holding one text block. */
export function textMessage(id: string, turnId: string, kind: UnifiedMessageKind, text: string) {
  return create(UnifiedMessageSchema, { id, turnId, kind, agentWorkId: "work", agent: "", timestamp: timestampFromDate(new Date(1700000000000)), payload: { case: "message", value: { content: [{ type: ContentBlockType.TEXT, text }] } } });
}
