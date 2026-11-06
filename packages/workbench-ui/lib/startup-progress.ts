import type { StartupProgress } from "@talgent/workbench-contracts/agent/public/v1/startup_pb";

// Revisions order committed Work projections, including completed progress.
// They do not establish native feedback ownership or ordering across processes.
export function newestStartupProgress(current: StartupProgress | undefined, incoming: StartupProgress | undefined) {
  if (!incoming) return current;
  if (!current || incoming.revision > current.revision) return incoming;
  return current;
}
