import { useState, type ReactNode } from "react";
import { Button, Dropdown, Kbd, Label, ListBox, Popover, SearchField, ToggleButton, ToggleButtonGroup } from "@heroui/react";
import { Brain, BrainCircuit, ChevronDown, Gauge, Paperclip, Plus, SquareSlash, Zap } from "lucide-react";
import type { ModelVendor } from "@talgent/workbench-contracts/agent/public/v1/model_pb";
import { modelVendors } from "../../lib/model-vendors";
import { thinkingLevelName as levelName } from "../../lib/thinking-levels";
import { startModelKey, type StartModel } from "../../lib/work-model";

const levelIcon = (level: string) => level === "low" ? Zap : level === "medium" ? Gauge : level === "high" ? Brain : BrainCircuit;

// A model list searches only once it is longer than a glance.
const searchableModels = 6;

export function ModelVendorIcon({ vendor }: { vendor: ModelVendor }) {
  const icon = modelVendors[vendor]?.icon;
  return icon ? <img className="wpb-product-icon" src={`/assets/product-icons/${icon}.svg`} alt="" /> : <Brain size={16} aria-hidden="true" />;
}

/** The composer's model trigger: the model's name and the thinking effort, or a status in its place. */
export function ModelTrigger({ name, effort, tentative = false, disabled }: { name: string; effort: string; tentative?: boolean; disabled?: boolean }) {
  return <Button size="sm" variant="ghost" aria-label="Model and thinking effort" className="wpb-model-trigger" isDisabled={disabled}>
    <span className="wpb-model-name">{name}</span><span className="wpb-trigger-effort" data-tentative={tentative || undefined}>{effort}</span><ChevronDown size={13} />
  </Button>;
}

/** One segmented choice of the model's thinking levels. */
export function ThinkingEffort({ levels, value, disabled, onChange }: { levels: string[]; value: string; disabled?: boolean; onChange: (level: string) => void }) {
  return <div className="wpb-effort">
    <p id="wpb-effort-label">Thinking effort</p>
    <ToggleButtonGroup aria-labelledby="wpb-effort-label" selectionMode="single" disallowEmptySelection fullWidth size="sm" isDisabled={disabled}
      selectedKeys={value ? new Set([value]) : new Set()} onSelectionChange={keys => { const next = [...keys][0]; if (next !== undefined && String(next) !== value) onChange(String(next)); }}>
      {levels.map((level, index) => { const Icon = levelIcon(level); return <ToggleButton key={level} id={level}>{index > 0 && <ToggleButtonGroup.Separator />}<Icon size={14} aria-hidden="true" />{levelName(level)}</ToggleButton>; })}
    </ToggleButtonGroup>
  </div>;
}

// Start Work chooses a model and its thinking effort; a running Work's model
// is fixed and uses its own card.
export function PromptModelControl({ models, model, thinking, disabled, loading, open, onOpenChange, onChange }: {
  models: StartModel[]; model?: StartModel; thinking: string; disabled?: boolean; loading?: boolean;
  open: boolean; onOpenChange: (open: boolean) => void; onChange: (model: StartModel, thinking: string) => void;
}) {
  const [query, setQuery] = useState("");
  const shown = models.filter(item => `${item.name} ${modelVendors[item.vendor]?.label ?? ""}`.toLowerCase().includes(query.trim().toLowerCase()));
  return <Popover isOpen={open} onOpenChange={value => { onOpenChange(value); if (value) setQuery(""); }}>
    <ModelTrigger name={model?.name ?? (loading ? "" : "Select model")} effort={model ? levelName(thinking) : ""} disabled={disabled} />
    <Popover.Content placement="bottom end" className="wpb-popover wpb-popover-narrow"><Popover.Dialog aria-label="Model settings">
      {models.length > searchableModels && <SearchField className="wpb-search" aria-label="Search models" value={query} onChange={setQuery} onKeyDown={event => { if (event.key === "Enter") event.preventDefault(); }}>
        <SearchField.Group><SearchField.SearchIcon /><SearchField.Input autoFocus placeholder="Search models" /><SearchField.ClearButton aria-label="Clear search" /></SearchField.Group>
      </SearchField>}
      <ListBox aria-label="Models" selectionMode="single" disallowEmptySelection selectedKeys={model ? [startModelKey(model)] : []} renderEmptyState={() => <p className="wpb-empty" role="status">No matches</p>}
        onSelectionChange={keys => { if (keys === "all") return; const next = models.find(item => keys.has(startModelKey(item))); if (next) onChange(next, next.thinkingLevels.includes(thinking) ? thinking : next.defaultThinkingLevel); }}>
        {shown.map(item => <ListBox.Item id={startModelKey(item)} key={startModelKey(item)} textValue={item.name} className="wpb-choice"><ModelVendorIcon vendor={item.vendor} /><span className="wpb-choice-copy"><Label>{item.name}</Label><span className="wpb-muted">{modelVendors[item.vendor]?.label}</span></span><ListBox.ItemIndicator /></ListBox.Item>)}
      </ListBox>
      {model && model.thinkingLevels.length > 0 && <ThinkingEffort levels={model.thinkingLevels} value={thinking} onChange={level => onChange(model, level)} />}
    </Popover.Dialog></Popover.Content>
  </Popover>;
}

/** The composer's "+": attach files where the Work takes them, or open the
 * slash menu of commands and Skills. */
export function PromptAddMenu({ disabled, attachDisabled, onAttach, onCommands }: { disabled?: boolean; attachDisabled?: boolean; onAttach?: () => void; onCommands?: () => void }) {
  return <Dropdown>
    <Button size="sm" variant="ghost" className="wpb-add" isIconOnly isDisabled={disabled} aria-label="Add context and options"><Plus size={22} strokeWidth={1.75} /></Button>
    <Dropdown.Popover placement="top start" className="wpb-popover wpb-popover-narrow">
      <Dropdown.Menu aria-label="Add to message" onAction={key => key === "attach" ? onAttach?.() : onCommands?.()} disabledKeys={[...(attachDisabled ? ["attach"] : []), ...(onCommands ? [] : ["commands"])]}>
        {onAttach && <Dropdown.Item id="attach" textValue="Attach files"><Paperclip size={16} aria-hidden="true" /><Label>Attach files</Label></Dropdown.Item>}
        <Dropdown.Item id="commands" textValue="Commands and Skills"><SquareSlash size={16} aria-hidden="true" /><Label>Commands & Skills</Label><Kbd className="wpb-kbd">/</Kbd></Dropdown.Item>
      </Dropdown.Menu>
    </Dropdown.Popover>
  </Dropdown>;
}

/** A popover's closing note, such as the scope of the changes made in it. */
export function PopoverFootnote({ children }: { children: ReactNode }) {
  return <p className="wpb-footnote">{children}</p>;
}
