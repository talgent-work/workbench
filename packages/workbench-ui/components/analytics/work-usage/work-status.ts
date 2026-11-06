import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import { useAgentWork } from "../../../lib/agent-work-service";

export type WorkUsageStatus = { state: string; waiting: boolean; updatedAt: Date | null };

export function workUsageStatus(work: { state: string; waiting: boolean; updatedAt?: Timestamp } | undefined): WorkUsageStatus | null {
  return work ? { state: work.state, waiting: work.waiting, updatedAt: work.updatedAt ? timestampDate(work.updatedAt) : null } : null;
}

/** The Work's own state, the authority on whether it works (AgentWork), as
 * its owner reads it. */
export function useOwnWorkUsageStatus(workId: string): WorkUsageStatus | null {
  return workUsageStatus(useAgentWork(workId).data?.work);
}

export function formatMillis(millis: number): string {
  if (millis < 1000) return `${Math.round(millis)}ms`;
  const seconds = millis / 1000;
  if (seconds < 60) return `${seconds.toFixed(1).replace(/\.0$/, "")}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${String(Math.round(seconds % 60)).padStart(2, "0")}s`;
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, "0")}m`;
}

export function formatClock(time: number, now = Date.now()): string {
  const date = new Date(time);
  const clock = date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
  return new Date(now).toDateString() === date.toDateString() ? clock : `${date.toLocaleDateString(undefined, { month: "short", day: "numeric" })} ${clock}`;
}

export function formatAgo(millis: number): string {
  if (millis < 60_000) return `${Math.max(0, Math.round(millis / 1000))}s ago`;
  if (millis < 3_600_000) return `${Math.floor(millis / 60_000)}m ago`;
  if (millis < 86_400_000) return `${Math.floor(millis / 3_600_000)}h ago`;
  return `${Math.floor(millis / 86_400_000)}d ago`;
}
