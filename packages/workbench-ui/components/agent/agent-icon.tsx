import type { CSSProperties } from "react";
import { Bot } from "lucide-react";
import { agentColors, agentGlyphs, agentIconOf, type AgentIconValue } from "../../lib/agent-icon";
import "./agent-icon.css";

// Decorative: the surrounding control or text names the Agent.
export function AgentIcon({ icon, size = 24, className }: { icon: Partial<AgentIconValue> | undefined; size?: number; className?: string }) {
  const { glyph, color } = agentIconOf(icon);
  const Glyph = agentGlyphs.find(item => item.id === glyph)?.Icon ?? Bot;
  const style = { "--agent-ink": agentColors[color], "--agent-icon-size": `${size}px` } as CSSProperties;
  return <span aria-hidden="true" className={className ? `agent-icon ${className}` : "agent-icon"} data-glyph={glyph} data-color={color} style={style}>
    <Glyph size={Math.round(size * 0.6)} />
  </span>;
}
