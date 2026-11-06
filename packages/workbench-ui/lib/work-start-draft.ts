import { skipToken, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { ParameterDrafts } from "./plugin-parameters";
import type { WorkStartInput } from "./start-work-request";

// Per-Work choices recorded as differences from the personal defaults. None of
// them is ever written back to a personal installation.
export interface WorkPluginChoice {
  /** Undefined keeps the default inclusion; true adds, false removes. */
  include?: boolean;
  values: ParameterDrafts;
}

export interface WorkStartDraft {
  instruction: string;
  model?: WorkStartInput["model"];
  thinkingLevel?: string;
  repositories: string[];
  plugins: Record<string, WorkPluginChoice>;
  /** Per-Work Connector inclusion that differs from the default. */
  connectors: Record<string, boolean>;
  agent: string;
  /** The chosen Computer; empty keeps the default, the first Cloud. */
  computer: string;
  /** The Harness choices for a Managed Computer; unset keeps their defaults. */
  harness?: WorkStartInput["harness"];
  files: Array<{ id: string; file: File; uploaded: boolean }>;
  submitting: boolean;
}

/** What a draft is for: one per `key`, starting from `instruction`. */
export type WorkStartDraftScope = { key: string; instruction: string };

// The session QueryClient is cleared on logout. Files remain in memory only;
// every entry point of one scope observes the same draft and in-flight
// submission guard.
export function useWorkStartDraft(scope: WorkStartDraftScope) {
  const client = useQueryClient();
  const key = ["work-start-draft", scope.key] as const;
  const { data } = useQuery<WorkStartDraft>({
    queryKey: key,
    queryFn: skipToken,
    initialData: () => ({ instruction: scope.instruction, repositories: [], plugins: {}, connectors: {}, agent: "", computer: "", files: [], submitting: false }),
    enabled: false,
    staleTime: Infinity,
    gcTime: Infinity,
    structuralSharing: false,
  });
  // The cache notifies observers on a later tick, but a controlled textarea must
  // take each keystroke at once: a value React restores even briefly ends an
  // input method's composition and commits the raw keystrokes. The instruction is
  // therefore held locally as well, and follows the cache when it changes elsewhere.
  const [instruction, setInstruction] = useState(data!.instruction);
  const [observed, setObserved] = useState(data!.instruction);
  if (data!.instruction !== observed) { setObserved(data!.instruction); setInstruction(data!.instruction); }
  function update(change: Partial<WorkStartDraft> | ((draft: WorkStartDraft) => WorkStartDraft)) {
    const next = client.setQueryData<WorkStartDraft>(key, previous => previous && (typeof change === "function" ? change(previous) : { ...previous, ...change }));
    if (next) { setObserved(next.instruction); setInstruction(next.instruction); }
  }
  return { draft: { ...data!, instruction }, update, current: () => client.getQueryData<WorkStartDraft>(key)! };
}
