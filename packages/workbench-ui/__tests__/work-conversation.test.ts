import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { UnifiedMessageKind, UnifiedMessageSchema } from "@talgent/contracts/dispatcher/public/v1/unified_message_pb";
import { expect, it } from "vitest";
import { WorkConversation } from "../lib/work-conversation";

function message(id: string, turnId: string, seconds: number) {
  return create(UnifiedMessageSchema, { id, agentWorkId: "work", agent: "", turnId, kind: UnifiedMessageKind.ASSISTANT, timestamp: timestampFromDate(new Date(seconds * 1000)), payload: { case: "message", value: { content: [{ text: id }] } } });
}

it("groups by native turn and keeps unrelated turns stable when late messages arrive", () => {
  const store = new WorkConversation("work");
  store.add([message("first", "one", 1), message("second", "two", 2)], "replay");
  const second = store.getSnapshot().turns[1];
  store.add([message("late", "one", 3)], "live");
  expect(store.getSnapshot().turns[0].messages.map(item => item.id)).toEqual(["first", "late"]);
  expect(store.getSnapshot().turns[1]).toBe(second);
  const unknown = message("unattributed", "", 4);
  unknown.agent = undefined;
  unknown.kind = UnifiedMessageKind.ERROR;
  unknown.payload = { case: "error", value: { $typeName: "dispatcher.public.v1.ErrorPayload", code: "api_failure", message: "Original error" } };
  const rootError = { ...unknown, id: "root-error", agent: "" };
  store.add([unknown, rootError, { ...message("child", "child-turn", 5), agent: "member" }], "live");
  // A process diagnostic has no Agent and stays out; the root Agent's error stays.
  expect(store.has("unattributed")).toBe(false);
  expect(store.getSnapshot().turns.at(-1)?.messages.map(item => item.id)).toEqual(["root-error"]);
  expect(store.getSnapshot().count).toBe(4);
});

it("anchors only newly delivered user messages and suppresses duplicate replay", () => {
  const store = new WorkConversation("work");
  const first = { ...message("first", "one", 1), kind: UnifiedMessageKind.USER };
  store.add([first], "replay");
  expect(store.getSnapshot().newUserId).toBeUndefined();
  const next = { ...message("next", "two", 2), kind: UnifiedMessageKind.USER };
  store.add([next], "live");
  expect(store.getSnapshot().newUserId).toBe("next");
  const snapshot = store.getSnapshot();
  store.add([next, first], "replay");
  expect(store.getSnapshot()).toBe(snapshot);
});

it("bounds both directions of browsing without truncating original content", () => {
  const store = new WorkConversation("work", 3, 10000);
  store.add(Array.from({ length: 20 }, (_, i) => message(String(i), `turn-${i}`, i)), "live");
  expect(store.getSnapshot().count).toBe(3);
  expect(store.getSnapshot().turns.map(turn => turn.turnId)).toEqual(["turn-17", "turn-18", "turn-19"]);
  store.add([message("older", "older-turn", 1)], "older");
  expect(store.getSnapshot().turns.map(turn => turn.turnId)).toEqual(["older-turn", "turn-17", "turn-18"]);
  const oversized = message("huge", "huge-turn", 30);
  if (oversized.payload.case === "message") oversized.payload.value.content[0].text = "原文".repeat(10000);
  store.add([oversized], "live");
  expect(store.getSnapshot().count).toBe(1);
  expect(store.getSnapshot().turns[0].messages[0]).toBe(oversized);
  store.add([message("small", "next-turn", 31)], "live");
  expect(store.getSnapshot().count).toBe(1);
  expect(store.getSnapshot().textBytes).toBeLessThan(10000);
});
