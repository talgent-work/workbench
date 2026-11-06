import { equals } from "@bufbuild/protobuf";
import { UnifiedMessageKind, UnifiedMessageSchema, type UnifiedMessage } from "@talgent-work/workbench-contracts/dispatcher/public/v1/unified_message_pb";

export interface WorkTurn {
  key: string;
  turnId: string;
  messages: readonly UnifiedMessage[];
}

interface ConversationSnapshot {
  turns: readonly WorkTurn[];
  count: number;
  textBytes: number;
  newUserId?: string;
  olderEvicted: boolean;
}

const emptySnapshot: ConversationSnapshot = { turns: [], count: 0, textBytes: 0, olderEvicted: false };

// The view owns a bounded message window. A frame updates only its actual turn;
// it does not reinterpret or sort the complete transcript on every delivery.
export class WorkConversation {
  private messages = new Map<string, UnifiedMessage>();
  private turns = new Map<string, WorkTurn>();
  private order: UnifiedMessage[] = [];
  private listeners = new Set<() => void>();
  private snapshot = emptySnapshot;
  private bytes = 0;

  constructor(private workId: string, private limit = 500, private byteLimit = 8 * 1024 * 1024) {}
  getSnapshot = () => this.snapshot;
  has = (id: string) => this.messages.has(id);
  getOldest = () => this.order[0];
  getNewest = () => this.order.at(-1);
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };

  add(messages: readonly UnifiedMessage[], source: "live" | "replay" | "history" | "older") {
    let olderEvicted = source === "older" ? false : this.snapshot.olderEvicted;
    let changed = olderEvicted !== this.snapshot.olderEvicted;
    let newUserId = this.snapshot.newUserId;
    for (const message of messages) {
      // The root Agent's messages only: process diagnostics carry no Agent and stay out.
      if (message.agentWorkId !== this.workId || message.agent !== "" || !message.id || !message.payload.case) continue;
      const previous = this.messages.get(message.id);
      // A history request may finish after a newer live version of the same ID.
      if (previous && (source === "history" || source === "older")) continue;
      if (previous && equals(UnifiedMessageSchema, previous, message)) continue;
      if (previous) this.remove(previous);
      this.messages.set(message.id, message);
      this.bytes += textBytes(message);
      insert(this.order, message);
      const key = turnKey(message);
      const turn = this.turns.get(key);
      const items = turn ? turn.messages.slice() : [];
      insert(items, message);
      this.turns.set(key, { key, turnId: message.turnId, messages: items });
      if (!previous && source === "live" && message.kind === UnifiedMessageKind.USER) newUserId = message.id;
      changed = true;
    }
    if (!changed) return;
    // ponytail: retain one oversized original message intact; a future lazy
    // text-body contract is required to bound that single-message case too.
    while (this.order.length > 1 && (this.order.length > this.limit || this.bytes > this.byteLimit)) {
      this.remove(source === "older" ? this.order[this.order.length - 1] : this.order[0]);
      if (source !== "older") olderEvicted = true;
    }
    const keys = new Set(this.order.map(turnKey));
    this.snapshot = { turns: [...keys].map(key => this.turns.get(key)!), count: this.messages.size, textBytes: this.bytes, newUserId, olderEvicted };
    this.listeners.forEach(listener => listener());
  }

  private remove(message: UnifiedMessage) {
    this.messages.delete(message.id);
    this.bytes -= textBytes(message);
    const index = this.order.findIndex(item => item.id === message.id);
    if (index >= 0) this.order.splice(index, 1);
    const key = turnKey(message);
    const turn = this.turns.get(key);
    if (!turn) return;
    const messages = turn.messages.filter(item => item.id !== message.id);
    if (messages.length) this.turns.set(key, { ...turn, messages });
    else this.turns.delete(key);
  }
}

function turnKey(message: UnifiedMessage) {
  // A missing native association is displayed independently, never attached
  // to the nearest user input or assigned a fabricated turn identity.
  return message.turnId ? `turn:${message.turnId}` : `message:${message.id}`;
}

function insert(messages: UnifiedMessage[], message: UnifiedMessage) {
  let low = 0, high = messages.length;
  while (low < high) {
    const middle = (low + high) >>> 1;
    if (compare(message, messages[middle]) < 0) high = middle; else low = middle + 1;
  }
  messages.splice(low, 0, message);
}

function compare(a: UnifiedMessage, b: UnifiedMessage) {
  const seconds = (a.timestamp?.seconds ?? 0n) - (b.timestamp?.seconds ?? 0n);
  if (seconds !== 0n) return seconds < 0n ? -1 : 1;
  const nanos = (a.timestamp?.nanos ?? 0) - (b.timestamp?.nanos ?? 0);
  return nanos || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

// Account for retained strings without allocating a second serialized body.
function textBytes(message: UnifiedMessage) {
  let characters = message.id.length + message.agentWorkId.length + message.turnId.length + (message.agent?.length ?? 0);
  const payload = message.payload;
  if (payload.case === "message") {
    for (const block of payload.value.content) characters += block.text.length + block.mimeType.length;
  } else if (payload.case) {
    for (const value of Object.values(payload.value)) if (typeof value === "string") characters += value.length;
  }
  return characters * 2;
}
