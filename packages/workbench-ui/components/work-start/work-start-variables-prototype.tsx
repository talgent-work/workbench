// PROTOTYPE ONLY: three ways the New work tray could show which Personal
// Variables this Work enables, switchable with ?variant=A|B|C on /. Every
// variable starts enabled; the Work keeps the ones left enabled. Nothing
// reaches a backend. Throw away once a variant wins.
import { useState } from "react";
import { Button, Popover, Switch } from "@heroui/react";
import { Braces } from "lucide-react";
import { Link } from "react-router-dom";
import { PrototypeSwitcher, usePrototypeVariant } from "../prototype/prototype-switcher";
import { usePrototypeVariables } from "../prototype/prototype-variables-store";
import { CheckRow, ScopeNote, Search, TrayPopover } from "./work-start-tray";

const variants = [{ key: "A", name: "Checklist card" }, { key: "B", name: "All, with exceptions" }, { key: "C", name: "Chips in the tray" }];

/** Mounted at the end of the tray while the prototype runs. */
export function WorkStartVariablesPrototype({ disabled }: { disabled: boolean }) {
  const variables = usePrototypeVariables().map(variable => variable.name);
  const [off, setOff] = useState<string[]>([]);
  const variant = usePrototypeVariant(variants.map(item => item.key));
  const enabled = variables.filter(name => !off.includes(name));
  const toggle = (name: string) => setOff(current => current.includes(name) ? current.filter(item => item !== name) : [...current, name]);
  const props = { variables, enabled, toggle, setOff, disabled };
  return <>
    {variant === "A" && <VariantA {...props} />}
    {variant === "B" && <VariantB {...props} />}
    {variant === "C" && <VariantC {...props} />}
    <span className="sr-only" role="status">{`Enabled for this Work: ${enabled.join(", ") || "none"}`}</span>
    <PrototypeSwitcher variants={variants} />
  </>;
}

type Props = { variables: string[]; enabled: string[]; toggle: (name: string) => void; setOff: (off: string[]) => void; disabled: boolean };

const ManageLink = () => <Link to="/profile" className="wst-manage-link text-[13px] text-muted underline-offset-2 hover:underline">Manage variables in Profile</Link>;
const Empty = () => <div className="wst-picker-empty"><p>No variables yet. Variables you add in Profile show here for every new Work.</p><ManageLink /></div>;
const countLabel = (enabled: number, total: number) => total === 0 ? "Variables" : enabled === total ? `${total} ${total === 1 ? "variable" : "variables"}` : `${enabled} of ${total} variables`;

// A: one more tray card, like Plugins and Connectors: a checklist grouped by
// source, searchable once it grows.
function VariantA({ variables, enabled, toggle, disabled }: Props) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const shown = variables.filter(name => name.toLowerCase().includes(query.trim().toLowerCase()));
  return <TrayPopover label="Variables" value={countLabel(enabled.length, variables.length)} icon={<Braces size={16} />} disabled={disabled} open={open} onOpenChange={value => { setOpen(value); if (value) setQuery(""); }}>
    {variables.length === 0 ? <Empty /> : <>
      {variables.length > 6 && <Search label="Search variables" value={query} onChange={setQuery} />}
      <div className="wst-scroll">
        <div className="wst-rows" role="group" aria-label="Personal">
          <p className="wst-group-title">Personal</p>
          {shown.map(name => <CheckRow key={name} label={`Enable ${name} for this Work`} checked={enabled.includes(name)} struck={!enabled.includes(name)} title={name} onChange={() => toggle(name)} />)}
          {shown.length === 0 && <p className="wpb-empty" role="status">No variables match.</p>}
        </div>
      </div>
      <div className="flex items-center justify-between gap-3 px-3 pb-2"><ManageLink /></div>
      <ScopeNote />
    </>}
  </TrayPopover>;
}

// B: the card leads with one switch, all variables on; turning it off lists
// each variable to keep or drop.
function VariantB({ variables, enabled, toggle, setOff, disabled }: Props) {
  const [open, setOpen] = useState(false);
  const all = enabled.length === variables.length;
  const [picking, setPicking] = useState(false);
  const value = variables.length === 0 ? "Variables" : all ? "All variables" : `${variables.length - enabled.length} off`;
  return <TrayPopover label="Variables" value={value} icon={<Braces size={16} />} disabled={disabled} open={open} onOpenChange={setOpen}>
    {variables.length === 0 ? <Empty /> : <>
      <div className="wst-switch-row">
        <Switch size="sm" className="w-full" isSelected={all && !picking} onChange={on => { if (on) { setOff([]); setPicking(false); } else setPicking(true); }}>
          <Switch.Content className="flex !flex-row w-full items-center justify-between gap-3 text-[14px]">Use all my variables<Switch.Control><Switch.Thumb /></Switch.Control></Switch.Content>
        </Switch>
      </div>
      {all && !picking
        ? <p className="px-3 pb-2 text-[13px] text-muted">{variables.join(", ")}</p>
        : <div className="wst-rows px-1" role="group" aria-label="Variables for this Work">
          {variables.map(name => <div key={name} className="wst-switch-row">
            <Switch size="sm" className="w-full" isSelected={enabled.includes(name)} onChange={() => toggle(name)}>
              <Switch.Content className="flex !flex-row w-full items-center justify-between gap-3"><span className="font-mono text-[13px]">{name}</span><Switch.Control><Switch.Thumb /></Switch.Control></Switch.Content>
            </Switch>
          </div>)}
        </div>}
      <div className="px-3 pb-2"><ManageLink /></div>
      <ScopeNote />
    </>}
  </TrayPopover>;
}

// C: no card: each variable is its own tray chip, pressed to drop it. Past
// three, the rest fold into a "+N" card.
function VariantC({ variables, enabled, toggle, disabled }: Props) {
  const [open, setOpen] = useState(false);
  if (variables.length === 0) return <Button size="sm" variant="ghost" className="wpb-config-trigger" isDisabled={disabled} aria-label="Variables: none"><Braces size={16} /><span>No variables</span></Button>;
  const chip = (name: string) => {
    const on = enabled.includes(name);
    return <Button key={name} size="sm" variant="ghost" className="wpb-config-trigger" isDisabled={disabled} aria-pressed={on} aria-label={`${name}, ${on ? "enabled" : "off"} for this Work`} onPress={() => toggle(name)}>
      <Braces size={14} className={on ? "" : "opacity-40"} /><span className={`font-mono text-[12px] ${on ? "" : "text-muted line-through"}`}>{name}</span>
    </Button>;
  };
  const rest = variables.slice(3);
  return <>
    {variables.slice(0, 3).map(chip)}
    {rest.length > 0 && <Popover isOpen={open} onOpenChange={setOpen}>
      <Button size="sm" variant="ghost" className="wpb-config-trigger" isDisabled={disabled} aria-label={`${rest.length} more variables`}><span>+{rest.length}</span></Button>
      <Popover.Content placement="bottom start" className="wpb-popover wpb-popover-tray"><Popover.Dialog aria-label="More variables">
        <div className="wst-rows">{rest.map(name => <CheckRow key={name} label={`Enable ${name} for this Work`} checked={enabled.includes(name)} struck={!enabled.includes(name)} title={name} onChange={() => toggle(name)} />)}</div>
        <ScopeNote />
      </Popover.Dialog></Popover.Content>
    </Popover>}
  </>;
}
