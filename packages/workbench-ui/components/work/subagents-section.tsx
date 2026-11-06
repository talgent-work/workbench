import { useEffect, useState } from "react";
import type { SubAgent } from "../../lib/subagents";
import { RailSection } from "../rail/rail-section";
import { SubAgentItem } from "./subagent-item";
import "./subagents-section.css";

const leaveMs = 200;

// SubAgents arrive only on the Work's stream, so a reader who cannot read the
// conversation has none. Undefined is no report yet; the first report is
// where changes count from. The section opens when a SubAgent starts running
// while the rail is in view, as Progress opens on a new report; one finishing
// never opens it.
export function useSubAgentsSection({ agents, scope, visible }: { agents: SubAgent[] | undefined; scope: string; visible: boolean }) {
  const running = agents?.filter(agent => agent.status === "running").map(agent => agent.id);
  const [open, setOpen] = useState(false);
  const [seen, setSeen] = useState({ scope, running });
  if (seen.scope !== scope) {
    setSeen({ scope, running });
    setOpen(false);
  } else if (running && running.join() !== seen.running?.join()) {
    setSeen({ scope, running });
    if (seen.running && visible && running.some(id => !seen.running!.includes(id))) setOpen(true);
  }
  // Cleared SubAgents leave with their exit motion before the section goes.
  const list = agents ?? [];
  const [shown, setShown] = useState(list);
  const leaving = !list.length && shown.length > 0;
  if (list.length && shown !== list) setShown(list);
  useEffect(() => {
    if (!leaving) return;
    const timer = window.setTimeout(() => setShown([]), leaveMs);
    return () => window.clearTimeout(timer);
  }, [leaving]);
  if (!shown.length) return null;
  const runningCount = running?.length ?? 0;
  return <RailSection title="Agents" summary={leaving ? undefined : runningCount ? `${runningCount} running` : `${shown.length}`} open={open} onOpenChange={setOpen}>
    <ul className="sa-row" aria-label="SubAgents">{shown.map(agent => <SubAgentItem key={agent.id} agent={agent} leaving={leaving} />)}</ul>
  </RailSection>;
}
