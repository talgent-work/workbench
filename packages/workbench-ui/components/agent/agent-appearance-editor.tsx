import { useId, useState, type FormEvent } from "react";
import { Alert, Button, Card, FieldError, Input, Label, TextField, ToggleButton, ToggleButtonGroup, type Selection } from "@heroui/react";
import { Check } from "lucide-react";
import { AgentIcon } from "./agent-icon";
import { agentColors, agentGlyphs, type AgentAppearanceValue, type AgentColor } from "../../lib/agent-icon";

const MAX_NAME = 40;

function nameProblem(name: string) {
  if (!name) return "Enter a name.";
  if (Array.from(name).length > MAX_NAME) return `Use at most ${MAX_NAME} characters.`;
  return "";
}

/** Names an Agent and picks its icon. Every place the Agent appears shows what
 * is saved here. The caller owns the save: `saving` while it runs and `error`
 * when it failed, which `onDiscard` clears. */
export function AgentAppearanceEditor({ saved, label, hint, saving, error, onSave, onDiscard }: {
  saved: AgentAppearanceValue; label: string; hint: string; saving: boolean; error: string;
  onSave: (appearance: AgentAppearanceValue) => void; onDiscard: () => void;
}) {
  const id = useId();
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
    if (!dirty || problem || saving) return;
    onSave({ ...draft, name });
  };
  const discard = () => { onDiscard(); setDraft(saved); };
  return (
    <Card className="mk-group">
      <form onSubmit={save} aria-label={label}>
        <Card.Content className="grid gap-5 p-5">
          <div className="flex items-start gap-4">
            <AgentIcon icon={draft.icon} size={56} />
            <TextField className="min-w-0 flex-1" value={draft.name} onChange={value => setDraft(current => ({ ...current, name: value }))} isInvalid={Boolean(problem)} isDisabled={saving}>
              <Label>Name</Label>
              <Input />
              <FieldError>{problem}</FieldError>
            </TextField>
          </div>
          <div className="grid gap-2">
            <span id={`${id}-glyph`} className="text-[13px] font-medium">Icon</span>
            <ToggleButtonGroup aria-labelledby={`${id}-glyph`} selectionMode="single" disallowEmptySelection isDetached isDisabled={saving} size="sm" className="flex-wrap justify-start"
              selectedKeys={new Set([draft.icon.glyph])} onSelectionChange={pick("glyph")}>
              {agentGlyphs.map(({ id, label: glyph, Icon }) => <ToggleButton key={id} id={id} isIconOnly aria-label={glyph} variant="ghost"><Icon size={16} aria-hidden="true" /></ToggleButton>)}
            </ToggleButtonGroup>
          </div>
          <div className="grid gap-2">
            <span id={`${id}-color`} className="text-[13px] font-medium">Color</span>
            <ToggleButtonGroup aria-labelledby={`${id}-color`} selectionMode="single" disallowEmptySelection isDetached isDisabled={saving} size="sm" className="flex-wrap justify-start"
              selectedKeys={new Set([draft.icon.color])} onSelectionChange={pick("color")}>
              {(Object.keys(agentColors) as AgentColor[]).map(color => (
                <ToggleButton key={color} id={color} isIconOnly aria-label={color[0].toUpperCase() + color.slice(1)} variant="ghost">
                  <span aria-hidden="true" className="agent-color-swatch" style={{ background: agentColors[color] }}>{draft.icon.color === color && <Check size={12} strokeWidth={3} />}</span>
                </ToggleButton>
              ))}
            </ToggleButtonGroup>
          </div>
          {error && (
            <Alert status="danger">
              <Alert.Indicator />
              <Alert.Content><Alert.Title>{error}</Alert.Title></Alert.Content>
            </Alert>
          )}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="mk-muted min-w-[16rem] flex-1 text-[13px]">{hint}</p>
            <div className="ml-auto flex gap-2">
              <Button size="sm" variant="ghost" isDisabled={!dirty || saving} onPress={discard}>Discard</Button>
              <Button size="sm" type="submit" isDisabled={!dirty || Boolean(problem)} isPending={saving}>{saving ? "Saving" : "Save"}</Button>
            </div>
          </div>
        </Card.Content>
      </form>
    </Card>
  );
}
