// PROTOTYPE (Applications): what a Work of the Application starts with when its start
// request names none: the model with its thinking level and the Startup Agent.
// The controls are the New work page's own.
import { useState } from "react";
import { Card } from "@heroui/react";
import { AlertTriangle } from "lucide-react";
import { PromptModelControl } from "../../../components/composer/prompt-bar-controls";
import { AgentPicker } from "../../../components/work-start/work-start-tray";
import type { Capability } from "../../../lib/work-start-loadout";
import { pluginOf, sampleModels } from "./proto-catalog";
import { useProtoStore, type ProtoApplication } from "./proto-data";
import { Section } from "./proto-parts";
import "../../../components/composer/work-start.css";
import "../../../components/work-start/work-start-tray.css";
import "./application-switcher.css";

export function WorkDefaults({ application, compact = false }: { application: ProtoApplication; compact?: boolean }) {
  const store = useProtoStore();
  const [open, setOpen] = useState<"model" | "agent" | null>(null);
  const { defaults } = application;
  // An absent model is the first Model Configuration, as for a start request without one.
  const model = sampleModels.find(item => item.configurationId === defaults.modelId) ?? sampleModels[0];
  const thinking = model.thinkingLevels.includes(defaults.thinking) ? defaults.thinking : model.defaultThinkingLevel;
  const agents: Capability[] = application.plugins.filter(item => item.enabled).flatMap(item => {
    const plugin = pluginOf(item.slug);
    return (plugin.agentNames ?? []).map(name => ({ name: `${plugin.slug}:${name}`, description: `${plugin.name}’s Agent`, argumentHint: "", plugin: plugin.name }));
  });
  const agentGone = Boolean(defaults.startupAgent) && !agents.some(agent => agent.name === defaults.startupAgent);
  const row = (label: string, hint: string, control: React.ReactNode) => <li className={compact ? "!flex-col !items-start !gap-1" : "!items-center"}>
    <span className={compact ? "mk-row-text text-[13px] text-muted" : "mk-row-text min-w-0 flex-1"}><strong>{label}</strong>{!compact && <span>{hint}</span>}</span>
    {control}
  </li>;
  return <Section id={`wd-${compact}`} title="Work defaults">
    {!compact && <p className="mk-note">Each Work {application.name} starts uses these unless its start request chooses others.</p>}
    <Card className="mk-group"><ul className="mk-group-list p-work-defaults" aria-label={`${application.name} Work defaults`}>
      {row("Model", "Thinking level changes during a Work; the model stays.",
        <PromptModelControl models={sampleModels} model={model} thinking={thinking} open={open === "model"} onOpenChange={value => setOpen(value ? "model" : null)}
          onChange={(next, level) => store.setDefaults(application.id, { modelId: next.configurationId, thinking: level })} />)}
      {row("Startup Agent", agents.length ? "The role a Work starts in, from the enabled Plugins." : "Enable a Plugin that declares an Agent to choose one.",
        <AgentPicker agents={agents} value={agentGone ? "" : defaults.startupAgent} disabled={false} open={open === "agent"} onOpenChange={value => setOpen(value ? "agent" : null)}
          onChange={agent => store.setDefaults(application.id, { startupAgent: agent })} />)}
    </ul></Card>
    {agentGone && <p className="flex items-center gap-1.5 text-[13px] text-warning"><AlertTriangle size={14} aria-hidden="true" />{defaults.startupAgent.split(":").at(-1)} is off with its Plugin, so Works start with the Default Agent.</p>}
  </Section>;
}
