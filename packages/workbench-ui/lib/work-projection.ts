import { Code, ConnectError } from "@connectrpc/connect";
import type { StartupProgress } from "@talgent/contracts/agent/public/v1/startup_pb";
import { isTerminalWorkLifecycle } from "./work-state-presentation";
import { newestStartupProgress } from "./startup-progress";

// The Work aggregate is the authority; the page shows a projection of it built
// from GetWork snapshots and live frames. Frames travel without delivery
// guarantees, so a delayed one must never replace a newer state.

export interface LiveWorkLifecycle {
  agentWorkId: string | null;
  lifecycle: string;
  updatedAt?: Date;
  generation: number;
  startupProgress?: StartupProgress;
}

export interface LiveWorkFrame {
  lifecycle: string;
  updatedAt?: Date;
  startupProgress?: StartupProgress;
}

/** Applies a live frame for agentWorkId. A frame older than the live state
 * already shown keeps that state; startup progress keeps its own revision order. */
export function nextLiveWorkLifecycle(state: LiveWorkLifecycle, agentWorkId: string | null, frame: LiveWorkFrame): LiveWorkLifecycle {
  const sameWork = state.agentWorkId === agentWorkId;
  const startupProgress = newestStartupProgress(sameWork ? state.startupProgress : undefined, frame.startupProgress);
  if (sameWork && frame.updatedAt && state.updatedAt && frame.updatedAt.getTime() < state.updatedAt.getTime()) {
    return startupProgress === state.startupProgress ? state : { ...state, startupProgress };
  }
  return {
    agentWorkId,
    lifecycle: frame.lifecycle,
    startupProgress,
    updatedAt: frame.updatedAt ?? (sameWork ? state.updatedAt : undefined),
    generation: sameWork && !isTerminalWorkLifecycle(frame.lifecycle)
      ? state.generation + 1
      : sameWork ? state.generation : 0,
  };
}

export type WorkSnapshotFreshness =
  | { status: "current" }
  | { status: "not_found" }
  | { status: "denied" }
  | { status: "unavailable" }
  | { status: "stale"; lastUpdatedAt: Date };

interface SnapshotQueryState {
  data: unknown;
  error: unknown;
  dataUpdatedAt: number;
}

/** Classifies the snapshot query behind the page. A refresh that fails after
 * a successful read keeps the last state and marks it stale; it never changes
 * the Work's lifecycle. Only an authoritative reader of the Work itself
 * (GetWork) answering NotFound means the Work does not exist. */
export function workSnapshotFreshness(query: SnapshotQueryState, authoritative = true): WorkSnapshotFreshness {
  if (!query.error) return { status: "current" };
  const code = ConnectError.from(query.error).code;
  if (authoritative && code === Code.NotFound) return { status: "not_found" };
  if (query.data === undefined) return code === Code.PermissionDenied ? { status: "denied" } : { status: "unavailable" };
  return { status: "stale", lastUpdatedAt: new Date(query.dataUpdatedAt) };
}

/** Work controls carry the version the caller observed; Agent answers Aborted
 * when the Work has changed since, so nothing is applied to a state the user
 * did not see. */
export function isStaleWorkControl(cause: unknown) {
  return ConnectError.from(cause).code === Code.Aborted;
}

export const staleWorkControlMessage = "This Work changed since you last saw it. Review its current state and try again.";

export const withdrawnModelMessage = "This Work's model is no longer offered, so it can't resume. Ask your administrator to restore the model, then try again.";

// Agent refuses Resume and Restart of a Work whose Model Configuration is withdrawn.
function isWithdrawnModel(cause: unknown) {
  const error = ConnectError.from(cause);
  return error.code === Code.FailedPrecondition && error.rawMessage.includes("model configuration is withdrawn");
}

export const turnRunningMessage = "This Work is in the middle of a turn. Cancel the turn, then stop the Work.";

// Agent stops a Work only between Turns.
export function isTurnRunning(cause: unknown) {
  const error = ConnectError.from(cause);
  return error.code === Code.FailedPrecondition && error.rawMessage.includes("a Turn is running");
}

export const inputPendingMessage = "This Work still has messages to finish. Try again once they are handled.";

// Agent closes a running Work only once its accepted input is finished; a
// stopped or failed Work closes at once and drops its buffered input.
export function isInputPending(cause: unknown) {
  const error = ConnectError.from(cause);
  return error.code === Code.FailedPrecondition && error.rawMessage.includes("still has accepted input to finish");
}

export const computerOfflineMessage = "This Work's computer is offline. Try again once it reconnects.";

// Agent refuses a start while the Work's Managed Computer is unreachable.
function isComputerUnreachable(cause: unknown) {
  const error = ConnectError.from(cause);
  return error.code === Code.FailedPrecondition && error.rawMessage.includes("the Computer is unreachable");
}

/** What a Work whose Resume is owed on reconnection says while its Managed
 * Computer is away: the messages sent meanwhile wait for the machine. */
export function awaitingReconnectionNotice(computerName: string | undefined) {
  return `${computerName || "This Work's computer"} is offline. Your messages are kept, and the Work resumes with them when it reconnects.`;
}

export function workControlErrorMessage(cause: unknown) {
  if (isStaleWorkControl(cause)) return staleWorkControlMessage;
  if (isComputerUnreachable(cause)) return computerOfflineMessage;
  if (isWithdrawnModel(cause)) return withdrawnModelMessage;
  if (isTurnRunning(cause)) return turnRunningMessage;
  if (isInputPending(cause)) return inputPendingMessage;
  return cause instanceof Error ? cause.message : "Work action failed.";
}
