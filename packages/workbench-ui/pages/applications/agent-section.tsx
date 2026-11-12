import { useState } from "react";
import { Alert, Button, Card, Skeleton } from "@heroui/react";
import { AlertTriangle } from "lucide-react";
import { AgentAppearanceEditor } from "../../components/agent/agent-appearance-editor";
import { AgentSoulEditor } from "../../components/agent/agent-soul-editor";
import { PromptModelControl } from "../../components/composer/prompt-bar-controls";
import { AgentPicker } from "../../components/work-start/work-start-tray";
import { InstallationsSection } from "./installations-section";
import type { Application } from "../../lib/application-service";
import {
  applicationAgentErrorMessage, useAgentWorkDefaults, useApplicationAgent, useApplicationAgentSoul, useChangeAgentWorkDefaults,
  useChangeApplicationAgentAppearance, useChangeApplicationAgentSoul, type AgentWorkDefaults, type ApplicationAgent, type WorkDefaults,
} from "../../lib/application-agent-service";
import type { Capability } from "../../lib/work-start-loadout";
import { startModels } from "../../lib/work-model";
import "../../components/composer/work-start.css";
import "../../components/work-start/work-start-tray.css";
import "./applications.css";

/** The Application's Agent: how it appears, its Soul, and what its Works
 * start with when their start request names nothing. Every Work of the
 * Application runs as this one Agent. */
export function AgentSection({ application }: { application: Application }) {
  const agent = useApplicationAgent(application.id);
  if (agent.isError) {
    return <Alert status="danger">
      <Alert.Indicator />
      <Alert.Content>
        <Alert.Title>{application.name}’s Agent could not be loaded.</Alert.Title>
        <Alert.Description>Its settings stay as they were.</Alert.Description>
      </Alert.Content>
      <Button size="sm" variant="secondary" isPending={agent.isFetching} onPress={() => void agent.refetch()}>Retry</Button>
    </Alert>;
  }
  if (!agent.data) return <Loading label={`Loading ${application.name}’s Agent…`} />;
  return <>
    <section className="mk-section" aria-labelledby="ap-appearance">
      <h3 id="ap-appearance" className="mk-section-title">Appearance</h3>
      <Appearance key={`${agent.data.name}:${agent.data.icon.glyph}:${agent.data.icon.color}`} application={application} agent={agent.data} />
    </section>
    <section className="mk-section" aria-labelledby="ap-soul">
      <h3 id="ap-soul" className="mk-section-title">Soul</h3>
      <Soul agentId={agent.data.agentId} />
    </section>
    <section className="mk-section" aria-labelledby="ap-work-defaults">
      <h3 id="ap-work-defaults" className="mk-section-title">Work defaults</h3>
      <p className="text-[14px] text-muted">Each Work {application.name} starts uses these unless its start request chooses others.</p>
      <Defaults agentId={agent.data.agentId} />
    </section>
    <Installations application={application} agentId={agent.data.agentId} />
  </>;
}

/** The Application's installations, once the Work defaults name the Plugins
 * every Work loads anyway. */
function Installations({ application, agentId }: { application: Application; agentId: string }) {
  const defaults = useAgentWorkDefaults(agentId);
  const required = new Set((defaults.data?.options?.plugins ?? []).filter(plugin => plugin.required).map(plugin => plugin.name));
  return <InstallationsSection application={application} required={required} />;
}

function Loading({ label }: { label: string }) {
  return <Card className="mk-group" aria-busy="true">
    <Card.Content className="grid gap-3 p-5">
      <p className="sr-only" role="status">{label}</p>
      <Skeleton className="h-10 rounded-lg" />
      <Skeleton className="h-6 w-2/3 rounded-lg" />
    </Card.Content>
  </Card>;
}

function Appearance({ application, agent }: { application: Application; agent: ApplicationAgent }) {
  const change = useChangeApplicationAgentAppearance(application.id);
  return <AgentAppearanceEditor saved={agent} label={`${application.name}’s Agent`} hint={`Every Work of ${application.name} runs as this Agent and shares its memory.`}
    saving={change.isPending} error={change.isError ? applicationAgentErrorMessage(change.error, "name or icon") : ""}
    onSave={appearance => change.mutate({ ...agent, ...appearance })} onDiscard={() => change.reset()} />;
}

function Soul({ agentId }: { agentId: string }) {
  const soul = useApplicationAgentSoul(agentId);
  const change = useChangeApplicationAgentSoul(agentId);
  if (soul.isError) {
    return <Alert status="danger">
      <Alert.Indicator />
      <Alert.Content><Alert.Title>The Soul could not be loaded.</Alert.Title></Alert.Content>
      <Button size="sm" variant="secondary" isPending={soul.isFetching} onPress={() => void soul.refetch()}>Retry</Button>
    </Alert>;
  }
  if (soul.data === undefined) return <Loading label="Loading the Soul…" />;
  return <AgentSoulEditor key={soul.data} saved={soul.data} placeholder="How this application’s Agent should talk and work" saving={change.isPending}
    error={change.isError ? applicationAgentErrorMessage(change.error, "Soul") : ""} onSave={value => change.mutate(value)} onDiscard={() => change.reset()} />;
}

function Defaults({ agentId }: { agentId: string }) {
  const defaults = useAgentWorkDefaults(agentId);
  if (defaults.isError) {
    return <Alert status="danger">
      <Alert.Indicator />
      <Alert.Content><Alert.Title>The Work defaults could not be loaded.</Alert.Title></Alert.Content>
      <Button size="sm" variant="secondary" isPending={defaults.isFetching} onPress={() => void defaults.refetch()}>Retry</Button>
    </Alert>;
  }
  if (!defaults.data) return <Loading label="Loading the Work defaults…" />;
  return <DefaultsEditor agentId={agentId} value={defaults.data} />;
}

/** The New work page's own model and Startup Agent controls; each choice saves
 * at once, as the New work page applies it at once. */
function DefaultsEditor({ agentId, value }: { agentId: string; value: AgentWorkDefaults }) {
  const change = useChangeAgentWorkDefaults(agentId);
  const [open, setOpen] = useState<"model" | "agent" | null>(null);
  const saved = change.isPending && change.variables ? change.variables : value.defaults;
  const models = startModels(value.options?.models ?? [], false);
  // No default model is the first offered one, as for a start that names none.
  const model = models.find(item => item.configurationId === saved.modelConfigurationId) ?? models[0];
  const thinking = model?.thinkingLevels.includes(saved.thinkingLevel) ? saved.thinkingLevel : model?.defaultThinkingLevel ?? "";
  const agents: Capability[] = (value.options?.plugins ?? []).flatMap(plugin => plugin.agents.map(agent => ({ name: agent.name, description: agent.description, argumentHint: "", plugin: plugin.displayName || plugin.name })));
  const agentGone = Boolean(saved.startupAgent) && !agents.some(agent => agent.name === saved.startupAgent);
  // A change saves what the controls show: a default withdrawn meanwhile is
  // dropped rather than sent back to be refused.
  const shown: WorkDefaults = {
    startupAgent: saved.startupAgent,
    modelConfigurationId: models.some(item => item.configurationId === saved.modelConfigurationId) ? saved.modelConfigurationId : "",
    thinkingLevel: thinking === saved.thinkingLevel ? saved.thinkingLevel : "",
  };
  const save = (next: Partial<WorkDefaults>) => change.mutate({ ...shown, ...next });
  return <>
    <Card className="mk-group">
      <ul className="mk-group-list ap-work-defaults" aria-label="Work defaults">
        <li className="!items-center">
          <span className="mk-row-text min-w-0 flex-1"><strong>Model</strong><span>The thinking effort can change during a Work; the model stays.</span></span>
          <PromptModelControl models={models} model={model} thinking={thinking} disabled={change.isPending || models.length === 0} open={open === "model"} onOpenChange={next => setOpen(next ? "model" : null)}
            onChange={(next, level) => save({ modelConfigurationId: next.configurationId, thinkingLevel: level })} />
        </li>
        <li className="!items-center">
          <span className="mk-row-text min-w-0 flex-1"><strong>Startup Agent</strong><span>{agents.length ? "The role a Work starts in, from the Plugins its Works load." : "None of the Plugins its Works load declares an Agent."}</span></span>
          <AgentPicker agents={agents} value={agentGone ? "" : saved.startupAgent} disabled={change.isPending} open={open === "agent"} onOpenChange={next => setOpen(next ? "agent" : null)}
            onChange={startupAgent => save({ startupAgent })} />
        </li>
      </ul>
    </Card>
    {agentGone && <p className="flex items-center gap-1.5 text-[13px] text-warning"><AlertTriangle size={14} aria-hidden="true" />{saved.startupAgent.split(":").at(-1)} isn’t declared by any Plugin its Works load. Choose another Startup Agent.</p>}
    {change.isError && <Alert status="danger">
      <Alert.Indicator />
      <Alert.Content><Alert.Title>{applicationAgentErrorMessage(change.error, "Work default")}</Alert.Title></Alert.Content>
    </Alert>}
  </>;
}
