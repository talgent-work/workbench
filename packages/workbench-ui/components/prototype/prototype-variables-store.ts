// PROTOTYPE ONLY: one in-memory list of Personal Variables shared by the
// Profile and New work prototypes, so an edit on one page shows on the other.
import { useSyncExternalStore } from "react";

export type PrototypeVariable = { name: string; value: string };

let variables: PrototypeVariable[] = [
  { name: "OPENAI_API_KEY", value: "sk-proj-7Hq2…demo" },
  { name: "ANTHROPIC_BASE_URL", value: "https://llm.example.test/anthropic" },
  { name: "DEPLOY_HOSTS", value: "web-1.example.test\nweb-2.example.test\nweb-3.example.test" },
];
const listeners = new Set<() => void>();

export function setPrototypeVariables(update: (current: PrototypeVariable[]) => PrototypeVariable[]) {
  variables = update(variables);
  listeners.forEach(listener => listener());
}

export function usePrototypeVariables(): PrototypeVariable[] {
  return useSyncExternalStore(listener => { listeners.add(listener); return () => listeners.delete(listener); }, () => variables);
}
