import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { ContentBlockType, UnifiedMessageSchema, type UnifiedMessage, type UnifiedMessageKind } from "@talgent/contracts/dispatcher/public/v1/unified_message_pb";
import type { Mock } from "vitest";

// A Work's live message stream for conversation tests: the watch mock yields
// each message delivered until the reader leaves.
export function streamFeed(watch: Mock) {
  let deliver: ((message: UnifiedMessage) => void) | undefined;
  watch.mockImplementation(async function* (_request: unknown, { signal }: { signal: AbortSignal }) {
    while (!signal.aborted) {
      let abort: () => void = () => {};
      const message = await new Promise<UnifiedMessage | undefined>(resolve => {
        deliver = resolve;
        abort = () => resolve(undefined);
        signal.addEventListener("abort", abort, { once: true });
      });
      signal.removeEventListener("abort", abort);
      if (!message) return;
      yield { payload: { case: "message", value: message } };
    }
  });
  return (message: UnifiedMessage) => deliver!(message);
}

/** A root message of the Work holding one text block. */
export function textMessage(id: string, turnId: string, kind: UnifiedMessageKind, text: string) {
  return create(UnifiedMessageSchema, { id, turnId, kind, agentWorkId: "work", agent: "", timestamp: timestampFromDate(new Date(1700000000000)), payload: { case: "message", value: { content: [{ type: ContentBlockType.TEXT, text }] } } });
}
