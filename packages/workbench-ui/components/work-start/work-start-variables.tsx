import { useState } from "react";
import { Button } from "@heroui/react";
import { Braces } from "lucide-react";
import { Link } from "react-router-dom";
import { enabledVariables } from "../../lib/work-start-loadout";
import { CheckRow, ScopeNote, Search, TrayPopover } from "./work-start-tray";

// Past this many variables the card offers a search.
const SEARCH_FROM = 7;

const ManageLink = () => <Link to="/profile" className="text-[13px] text-muted underline-offset-2 hover:underline">Manage variables in Profile</Link>;

const countLabel = (enabled: number, total: number) => total === 0 ? "Variables" : enabled === total ? `${total} ${total === 1 ? "variable" : "variables"}` : `${enabled} of ${total} variables`;

// Which of the person's Personal Variables this Work enables, grouped by where
// they come from (today only Personal). Every variable starts enabled; the
// Work keeps the ones left on, and Profile owns the variables themselves.
export function VariablesMenu({ variables, loading, failed, off, disabled, open, onOpenChange, onChange, onRetry }: {
  variables: string[]; loading: boolean; failed: boolean; off: string[]; disabled: boolean;
  open: boolean; onOpenChange: (open: boolean) => void; onChange: (off: string[]) => void; onRetry: () => void;
}) {
  const [query, setQuery] = useState("");
  const enabled = enabledVariables(variables, off);
  const shown = variables.filter(name => name.toLowerCase().includes(query.trim().toLowerCase()));
  const toggle = (name: string) => onChange(off.includes(name) ? off.filter(item => item !== name) : [...off, name]);
  return <TrayPopover label="Variables" value={countLabel(enabled.length, variables.length)} icon={<Braces size={16} />} attention={failed} disabled={disabled} open={open} onOpenChange={value => { onOpenChange(value); if (value) setQuery(""); }}>
    {loading ? <p className="wpb-empty" role="status">Loading variables…</p>
      : failed ? <p className="wpb-empty" role="alert">Variables couldn’t be loaded. <Button size="sm" variant="ghost" onPress={onRetry}>Retry</Button></p>
      : variables.length === 0 ? <div className="wst-picker-empty"><p>No variables yet. Variables you add in Profile show here for every new Work.</p><ManageLink /></div>
      : <>
        {variables.length >= SEARCH_FROM && <Search label="Search variables" value={query} onChange={setQuery} />}
        <div className="wst-scroll">
          <div className="wst-rows" role="group" aria-label="Personal">
            <p className="wst-group-title">Personal</p>
            {shown.map(name => <CheckRow key={name} label={`Enable ${name} for this Work`} checked={!off.includes(name)} struck={off.includes(name)} title={name} onChange={() => toggle(name)} />)}
            {shown.length === 0 && <p className="wpb-empty" role="status">No variables match.</p>}
          </div>
        </div>
        <div className="flex items-center justify-between gap-3 px-3 pb-2"><ManageLink /></div>
        <ScopeNote />
      </>}
  </TrayPopover>;
}
