import { useState, type FormEvent } from "react";
import { Alert, Button, Card, FieldError, Input, Label, Skeleton, TextField, ToggleButton, ToggleButtonGroup, type Selection } from "@heroui/react";
import { Check } from "lucide-react";
import { AgentIcon } from "../agent/agent-icon";
import { agentColors, agentGlyphs, type AgentColor } from "../../lib/agent-icon";
import { agentAppearanceErrorMessage, useChangeMyWorkAgentAppearance, useMyWorkAgent, type MyWorkAgent } from "../../lib/agent-appearance-service";

const MAX_NAME = 40;

// Profile › Your Work Agent: the person names their Work Agent and picks its
// icon. Every place the Agent appears shows what is saved here.
export function WorkAgentAppearance() {
  const agent = useMyWorkAgent();
  return (
    <section className="mk-section" aria-labelledby="pf-agent">
      <h3 id="pf-agent" className="mk-section-title">Your Work Agent</h3>
      {agent.data ? <AppearanceEditor key={`${agent.data.agentId}:${agent.data.name}:${agent.data.icon.glyph}:${agent.data.icon.color}`} saved={agent.data} />
        : agent.isError ? (
          <Alert status="danger">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Title>Your Work Agent could not be loaded.</Alert.Title>
              <Alert.Description>Its name and icon stay as they were.</Alert.Description>
            </Alert.Content>
            <Button size="sm" variant="secondary" isPending={agent.isFetching} onPress={() => void agent.refetch()}>Retry</Button>
          </Alert>
        ) : (
          <Card className="mk-group" aria-busy="true">
            <Card.Content className="flex items-center gap-4 p-5">
              <p className="sr-only" role="status">Loading your Work Agent…</p>
              <Skeleton className="size-14 rounded-2xl" />
              <div className="grid flex-1 gap-2"><Skeleton className="h-3 w-16 rounded" /><Skeleton className="h-9 rounded-lg" /></div>
            </Card.Content>
          </Card>
        )}
    </section>
  );
}

function nameProblem(name: string) {
  if (!name) return "Enter a name.";
  if (Array.from(name).length > MAX_NAME) return `Use at most ${MAX_NAME} characters.`;
  return "";
}

function AppearanceEditor({ saved }: { saved: MyWorkAgent }) {
  const change = useChangeMyWorkAgentAppearance();
  const [draft, setDraft] = useState(saved);
  const name = draft.name.trim();
  const problem = nameProblem(name);
  const dirty = name !== saved.name || draft.icon.glyph !== saved.icon.glyph || draft.icon.color !== saved.icon.color;
  const pick = (part: "glyph" | "color") => (keys: Selection) => {
    const id = [...keys][0];
    if (id !== undefined) setDraft(current => ({ ...current, icon: { ...current.icon, [part]: String(id) } }));
  };
  const save = (event: FormEvent) => {
    event.preventDefault();
    if (!dirty || problem || change.isPending) return;
    change.mutate({ ...draft, name });
  };
  const discard = () => { change.reset(); setDraft(saved); };
  return (
    <Card className="mk-group">
      <form onSubmit={save} aria-label="Your Work Agent">
        <Card.Content className="grid gap-5 p-5">
          <div className="flex items-start gap-4">
            <AgentIcon icon={draft.icon} size={56} />
            <TextField className="min-w-0 flex-1" value={draft.name} onChange={value => setDraft(current => ({ ...current, name: value }))} isInvalid={Boolean(problem)} isDisabled={change.isPending}>
              <Label>Name</Label>
              <Input />
              <FieldError>{problem}</FieldError>
            </TextField>
          </div>
          <div className="grid gap-2">
            <span id="pf-agent-glyph" className="text-[13px] font-medium">Icon</span>
            <ToggleButtonGroup aria-labelledby="pf-agent-glyph" selectionMode="single" disallowEmptySelection isDetached isDisabled={change.isPending} size="sm" className="flex-wrap justify-start"
              selectedKeys={new Set([draft.icon.glyph])} onSelectionChange={pick("glyph")}>
              {agentGlyphs.map(({ id, label, Icon }) => <ToggleButton key={id} id={id} isIconOnly aria-label={label} variant="ghost"><Icon size={16} aria-hidden="true" /></ToggleButton>)}
            </ToggleButtonGroup>
          </div>
          <div className="grid gap-2">
            <span id="pf-agent-color" className="text-[13px] font-medium">Color</span>
            <ToggleButtonGroup aria-labelledby="pf-agent-color" selectionMode="single" disallowEmptySelection isDetached isDisabled={change.isPending} size="sm" className="flex-wrap justify-start"
              selectedKeys={new Set([draft.icon.color])} onSelectionChange={pick("color")}>
              {(Object.keys(agentColors) as AgentColor[]).map(color => (
                <ToggleButton key={color} id={color} isIconOnly aria-label={color[0].toUpperCase() + color.slice(1)} variant="ghost">
                  <span aria-hidden="true" className="agent-color-swatch" style={{ background: agentColors[color] }}>{draft.icon.color === color && <Check size={12} strokeWidth={3} />}</span>
                </ToggleButton>
              ))}
            </ToggleButtonGroup>
          </div>
          {change.isError && (
            <Alert status="danger">
              <Alert.Indicator />
              <Alert.Content><Alert.Title>{agentAppearanceErrorMessage(change.error)}</Alert.Title></Alert.Content>
            </Alert>
          )}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="mk-muted min-w-[16rem] flex-1 text-[13px]">Shown wherever your Work Agent appears, including comments it wrote before.</p>
            <div className="ml-auto flex gap-2">
              <Button size="sm" variant="ghost" isDisabled={!dirty || change.isPending} onPress={discard}>Discard</Button>
              <Button size="sm" type="submit" isDisabled={!dirty || Boolean(problem)} isPending={change.isPending}>{change.isPending ? "Saving" : "Save"}</Button>
            </div>
          </div>
        </Card.Content>
      </form>
    </Card>
  );
}
