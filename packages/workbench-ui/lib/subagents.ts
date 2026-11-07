import type { SubAgent as ProtoSubAgent } from "@talgent/contracts/agent/public/v1/subagents_pb";

// The SubAgents the main Agent of the Work's current Harness process delegated
// to, as the Work reports them; the page never derives a state itself.
export type SubAgentStatus = "running" | "completed" | "failed" | "stopped";
export interface SubAgent { id: string; type: string; task: string; status: SubAgentStatus }

const statuses: ReadonlySet<string> = new Set<SubAgentStatus>(["running", "completed", "failed", "stopped"]);

export function adaptSubAgents(items: ReadonlyArray<Pick<ProtoSubAgent, "id" | "type" | "task" | "status">> | undefined): SubAgent[] {
  return (items ?? []).flatMap(item => item.id && statuses.has(item.status) ? [{ id: item.id, type: item.type, task: item.task, status: item.status as SubAgentStatus }] : []);
}
