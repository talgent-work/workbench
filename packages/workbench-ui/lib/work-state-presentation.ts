import { Archive, Circle, CirclePause, CircleX, Clock3, LoaderCircle, Unplug, type LucideIcon } from "lucide-react";

export type WorkPresentation = { label: string; icon: LucideIcon; color: "default" | "accent" | "success" | "warning" | "danger"; description?: string };

export const workStatePresentation = {
  created: { label: "Created", icon: Circle, color: "default" },
  starting: { label: "Starting", icon: LoaderCircle, color: "accent" },
  working: { label: "Working", icon: LoaderCircle, color: "success" },
  stopping: { label: "Stopping", icon: CirclePause, color: "default" },
  stopped: { label: "Stopped", icon: CirclePause, color: "default" },
  failed: { label: "Failed", icon: CircleX, color: "danger" },
  closing: { label: "Closing", icon: LoaderCircle, color: "default" },
  closed: { label: "Closed", icon: Archive, color: "default" },
} as const;

/** A Working Work whose Turn awaits its owner's answer. Waiting is a fact
 * beside the lifecycle state, never one of its values. */
export const waitingPresentation = { label: "Waiting for your answer", icon: Clock3, color: "warning" } as const;

/** How long a Working or Stopping Work may stay disconnected before it
 * stops. Agent decides the stop; this only words it, so it follows
 * Agent's bound if that changes. */
const disconnectionGraceMinutes = 5;

/** Why a Stopped Work stopped, where the reason needs saying. */
const stopReasonLabels: Record<string, string> = { disconnected: "Disconnected" };

/** The line a stopped conversation shows for its stop reason, if any. */
const stopReasonNotices: Record<string, string> = { disconnected: `Stopped after being disconnected for ${disconnectionGraceMinutes} minutes.` };

export function stopReasonNotice(state: string, stateReason: string | undefined): string | undefined {
  return state === "stopped" ? stopReasonNotices[stateReason ?? ""] : undefined;
}

/** What happens to a Work in each state once its Disconnection lasts the
 * grace: a Working or Stopping Work stops, a Closing one is closed. A start
 * is not stopped for it. */
const disconnectionOutcome: Record<string, string> = { working: "stops", stopping: "stops", closing: "closes" };

function clockOf(at: Date): string {
  return at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/** What a Disconnection means for a Work in the given state: since when the
 * platform has lost sight of where it runs (its Managed Computer, or its
 * Cloud sandbox) and, counted from then, when the Work stops or closes
 * unless it reconnects. */
export function disconnectionNote(state: string, since: Date, now: number = Date.now()): string {
  const unreachable = `Disconnected since ${clockOf(since)}.`;
  const outcome = disconnectionOutcome[state];
  if (!outcome) return unreachable;
  const at = new Date(since.getTime() + disconnectionGraceMinutes * 60_000);
  const then = at.getTime() <= now ? `The Work ${outcome} now.` : `The Work ${outcome} at ${clockOf(at)} unless it reconnects by then.`;
  return `${unreachable} ${then}`;
}

/** The accessible name of a Work's status: its label and, when it has one,
 * what it means, so a screen reader hears what the tooltip shows. */
export function workStatusName(prefix: string, presentation: { label: string; description?: string }): string {
  return presentation.description ? `${prefix}${presentation.label}. ${presentation.description}` : `${prefix}${presentation.label}`;
}

export type PresentedWork = { state: string; waiting?: boolean; stateReason?: string; disconnectedSince?: Date | null };

/** A Work's presentation: its Waiting Turn, otherwise its state, qualified by
 * a Disconnection (a fact beside the state) or a stop reason. */
export function workPresentation(work: PresentedWork | undefined): WorkPresentation | undefined {
  if (!work) return undefined;
  const base: WorkPresentation | undefined = work.waiting ? waitingPresentation : workStatePresentation[work.state as keyof typeof workStatePresentation];
  if (!base) return undefined;
  if (work.disconnectedSince && !isTerminalWorkLifecycle(work.state)) {
    return { label: `${base.label} · Disconnected`, icon: Unplug, color: "warning", description: disconnectionNote(work.state, work.disconnectedSince) };
  }
  const reason = work.waiting || work.state !== "stopped" ? undefined : stopReasonLabels[work.stateReason ?? ""];
  return reason ? { ...base, label: `${base.label} · ${reason}` } : base;
}

export function isTerminalWorkLifecycle(lifecycle: string): boolean {
  return lifecycle === "stopped" || lifecycle === "failed" || lifecycle === "closed";
}
