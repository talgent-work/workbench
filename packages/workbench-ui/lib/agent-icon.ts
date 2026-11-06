import { Atom, Bird, Bot, Brain, Cat, Compass, Flame, Leaf, Rocket, Sparkles, Star, Wand2, type LucideIcon } from "lucide-react";

// A Work Agent's icon: one preset glyph on one preset color. Agent owns the
// preset ids; anything it does not know reads as the default green robot.
export const agentGlyphs: ReadonlyArray<{ id: string; label: string; Icon: LucideIcon }> = [
  { id: "bot", label: "Bot", Icon: Bot }, { id: "sparkles", label: "Sparkles", Icon: Sparkles },
  { id: "brain", label: "Brain", Icon: Brain }, { id: "rocket", label: "Rocket", Icon: Rocket },
  { id: "wand", label: "Wand", Icon: Wand2 }, { id: "cat", label: "Cat", Icon: Cat },
  { id: "bird", label: "Bird", Icon: Bird }, { id: "leaf", label: "Leaf", Icon: Leaf },
  { id: "flame", label: "Flame", Icon: Flame }, { id: "star", label: "Star", Icon: Star },
  { id: "compass", label: "Compass", Icon: Compass }, { id: "atom", label: "Atom", Icon: Atom },
];

// The label palette, as explicit inks: Tailwind v4 emits only the
// color variables a stylesheet uses.
export const agentColors = { slate: "#475569", blue: "#2563eb", violet: "#7c3aed", green: "#16a34a", amber: "#d97706", rose: "#e11d48" } as const;
export type AgentColor = keyof typeof agentColors;

export interface AgentIconValue { glyph: string; color: string }
export const defaultAgentIcon: AgentIconValue = { glyph: "bot", color: "green" };

export function agentIconOf(icon: Partial<AgentIconValue> | undefined): { glyph: string; color: AgentColor } {
  const glyph = agentGlyphs.some(item => item.id === icon?.glyph) ? icon!.glyph! : defaultAgentIcon.glyph;
  const color = (icon?.color && icon.color in agentColors ? icon.color : defaultAgentIcon.color) as AgentColor;
  return { glyph, color };
}
