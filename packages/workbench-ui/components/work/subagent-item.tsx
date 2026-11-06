import { Tooltip } from "@heroui/react";
import { CircleCheck, CircleSlash, CircleX, LoaderCircle } from "lucide-react";
import { AgentIcon } from "../agent/agent-icon";
import { agentColors } from "../../lib/agent-icon";
import type { SubAgent, SubAgentStatus } from "../../lib/subagents";

const statuses: Record<SubAgentStatus, { label: string; Icon: typeof LoaderCircle; tone: "success" | "danger" | "muted" }> = {
  running: { label: "Running", Icon: LoaderCircle, tone: "success" },
  completed: { label: "Completed", Icon: CircleCheck, tone: "muted" },
  failed: { label: "Failed", Icon: CircleX, tone: "danger" },
  stopped: { label: "Stopped", Icon: CircleSlash, tone: "muted" },
};

// A SubAgent keeps one look for its whole life: its color follows its Harness
// identity, its glyph its type, with the robot for every other type.
const colorKeys = Object.keys(agentColors);
const typeGlyphs: Record<string, string> = { Explore: "compass", Plan: "brain", fork: "atom" };
const hash = (text: string) => [...text].reduce((sum, char) => (sum * 31 + char.charCodeAt(0)) >>> 0, 7);
const iconOf = (agent: SubAgent) => ({ glyph: typeGlyphs[agent.type] ?? "bot", color: colorKeys[hash(agent.id) % colorKeys.length] });
const typeOf = (agent: SubAgent) => agent.type || "SubAgent";

// Status is carried by weight: running is full color with the orbit, an ended
// one recedes, and only failed and stopped carry a mark.
export function SubAgentItem({ agent, leaving }: { agent: SubAgent; leaving: boolean }) {
  const status = statuses[agent.status];
  const marked = agent.status === "failed" || agent.status === "stopped";
  return <li data-leaving={leaving || undefined}>
    <Tooltip delay={200} closeDelay={0}>
      <Tooltip.Trigger className="sa-trigger" tabIndex={0} aria-label={`${typeOf(agent)}, ${status.label}${agent.task ? `: ${agent.task}` : ""}`}>
        <span className="sa-avatar" data-status={agent.status}>
          <AgentIcon icon={iconOf(agent)} size={26} />
          {marked && <span className="sa-badge" data-tone={status.tone} aria-hidden="true"><status.Icon size={10} strokeWidth={2} /></span>}
        </span>
      </Tooltip.Trigger>
      <Tooltip.Content className="sa-tip">
        <span className="sa-tip-head"><AgentIcon icon={iconOf(agent)} size={20} /><strong>{typeOf(agent)}</strong></span>
        <span className="sa-tip-status" data-tone={status.tone}><status.Icon size={12} aria-hidden="true" />{status.label}</span>
        {agent.task && <p>{agent.task}</p>}
      </Tooltip.Content>
    </Tooltip>
  </li>;
}
