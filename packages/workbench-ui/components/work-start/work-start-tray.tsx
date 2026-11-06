import { useState, type ReactNode } from "react";
import { Button, Checkbox, Description, Label, ListBox, Popover, SearchField, Switch } from "@heroui/react";
import type { WorkStartRepository } from "@talgent-work/workbench-contracts/agent/public/v1/work_start_options_pb";
import { AlertTriangle, Bot, Cloud, GitBranch, Server, Shield } from "lucide-react";
import type { UsableComputer } from "../../lib/computer-service";
import { permissionModeOption } from "../../lib/permission-modes";
import type { WorkStartInput } from "../../lib/start-work-request";
import { providerName, useConnectProvider, useProviderConnection } from "../../lib/marketplace-connections";
import type { Capability } from "../../lib/work-start-loadout";
import { PermissionModeList } from "../composer/permission-mode-list";
import { PopoverFootnote } from "../composer/prompt-bar-controls";

/** A tray chip and its card. Every card shares one surface and row anatomy. */
export function TrayPopover({ label, value, icon, disabled, attention, open, onOpenChange, wide, children }: { label: string; value: string; icon: ReactNode; disabled: boolean; attention?: boolean; open: boolean; onOpenChange: (open: boolean) => void; wide?: boolean; children: ReactNode }) {
  return <Popover isOpen={open} onOpenChange={onOpenChange}>
    <Button size="sm" variant="ghost" className="wpb-config-trigger" isDisabled={disabled} aria-label={`${label}: ${value}`}>{icon}<span>{value}</span>{attention && <AlertTriangle size={13} className="text-warning" aria-label="Needs attention" />}</Button>
    <Popover.Content placement="bottom start" className={`wpb-popover ${wide ? "wpb-popover-wide" : "wpb-popover-tray"}`}><Popover.Dialog aria-label={`${label} for this Work`}>{children}</Popover.Dialog></Popover.Content>
  </Popover>;
}

export const ScopeNote = () => <PopoverFootnote>Applies to this Work only</PopoverFootnote>;

/** A check row: the control, the entry and its state, and an optional action. */
export function CheckRow({ label, checked, disabled, struck, title, secondary, warn, trailing, onChange }: { label: string; checked: boolean; disabled?: boolean; struck?: boolean; title: string; secondary?: string; warn?: boolean; trailing?: ReactNode; onChange: () => void }) {
  return <div className="wst-check-row" data-struck={struck || undefined}>
    <Checkbox aria-label={label} isSelected={checked} isDisabled={disabled} onChange={onChange} className="wst-check">
      <Checkbox.Content><Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>
        <span className="wst-check-copy"><span className="wst-check-title">{title}</span>{secondary && <span className={warn ? "text-warning" : "wpb-muted"}>{secondary}</span>}</span>
      </Checkbox.Content>
    </Checkbox>
    {trailing}
  </div>;
}

export function Search({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return <SearchField className="wpb-search" aria-label={label} value={value} onChange={onChange} onKeyDown={event => { if (event.key === "Enter") event.preventDefault(); }}>
    <SearchField.Group><SearchField.SearchIcon /><SearchField.Input autoFocus placeholder={label} /><SearchField.ClearButton aria-label="Clear search" /></SearchField.Group>
  </SearchField>;
}

// Where the Work runs. With a single choice, the one Cloud, the picker only
// states it; otherwise it lists every Cloud, then the person's Managed
// Computers. An offline Managed Computer refuses a start, so it can't be picked.
// With no selection, a remembered choice that is gone, it lists them to choose
// again.
export function ComputerPicker({ computers, selected, disabled, open, onOpenChange, onChange }: { computers?: UsableComputer[]; selected?: UsableComputer; disabled: boolean; open: boolean; onOpenChange: (open: boolean) => void; onChange?: (id: string) => void }) {
  if (!computers || !onChange || (computers.length < 2 && selected)) return <TrayPopover label="Computer" value="Cloud" icon={<Cloud size={16} />} disabled={disabled} open={open} onOpenChange={onOpenChange}>
    <ListBox aria-label="Computer" selectionMode="single" disallowEmptySelection selectedKeys={["cloud"]}>
      <ListBox.Item id="cloud" textValue="Cloud" className="wpb-choice"><Cloud size={16} /><span className="wpb-choice-copy"><Label>Cloud</Label><span className="wpb-muted">Managed by Talgent</span></span><ListBox.ItemIndicator /></ListBox.Item>
    </ListBox>
  </TrayPopover>;
  const icon = (computer: UsableComputer) => computer.managed ? <Server size={16} /> : <Cloud size={16} />;
  return <TrayPopover label="Computer" value={selected?.name ?? "Choose a Computer"} icon={selected ? icon(selected) : <Server size={16} />} attention={!selected?.reachable} disabled={disabled} open={open} onOpenChange={onOpenChange}>
    <ListBox aria-label="Computer" selectionMode="single" disallowEmptySelection selectedKeys={selected ? [selected.id] : []} disabledKeys={computers.filter(computer => !computer.reachable).map(computer => computer.id)}
      onSelectionChange={keys => { if (keys === "all" || !keys.size) return; onChange(String([...keys][0])); onOpenChange(false); }}>
      {computers.map(computer => <ListBox.Item key={computer.id} id={computer.id} textValue={computer.reachable ? computer.name : `${computer.name}, offline`} className="wpb-choice">
        {icon(computer)}<span className="wpb-choice-copy"><Label>{computer.name}</Label><span className="wpb-muted">{computer.managed ? "Managed Computer" : "Managed by Talgent"}</span></span>
        {computer.reachable ? <ListBox.ItemIndicator /> : <span className="wst-offline">Offline</span>}
      </ListBox.Item>)}
    </ListBox>
  </TrayPopover>;
}

// Repositories are what the assignee's GitHub authorization reaches now; the
// assignee's recently used ones come first. No binding step exists.
export function RepositoryPicker({ repositories, recent, unavailable, loading, selected, disabled, open, onOpenChange, onChange, onRetry, returnTo }: {
  repositories: WorkStartRepository[]; recent: string[]; unavailable: boolean; loading: boolean; selected: string[]; disabled: boolean;
  open: boolean; onOpenChange: (open: boolean) => void; onChange: (selected: string[]) => void; onRetry: () => void; returnTo: string;
}) {
  const [query, setQuery] = useState("");
  const github = useProviderConnection("github");
  const connect = useConnectProvider();
  const status = github.data?.state?.status;
  const byId = new Map(repositories.map(repository => [repository.id, repository]));
  const value = selected.length === 0 ? "Repository" : selected.length === 1 ? byId.get(selected[0])?.repoName ?? "1 repository" : `${selected.length} repositories`;
  const matches = repositories.filter(repository => repository.repoName.toLowerCase().includes(query.trim().toLowerCase()));
  const recentRows = recent.map(id => byId.get(id)).filter((item): item is WorkStartRepository => Boolean(item));
  const toggle = (id: string) => onChange(selected.includes(id) ? selected.filter(item => item !== id) : [...selected, id]);
  const row = (repository: WorkStartRepository) => <CheckRow key={repository.id} label={repository.repoName} checked={selected.includes(repository.id)} title={repository.repoName} secondary={repository.defaultBranch} onChange={() => toggle(repository.id)} />;
  const group = (title: string, items: WorkStartRepository[]) => items.length > 0 && <div className="wst-rows" role="group" aria-label={title}><p className="wst-group-title">{title}</p>{items.map(row)}</div>;
  const needsConnection = status !== undefined && status !== "active";
  return <TrayPopover label="Repositories" value={value} icon={<GitBranch size={16} />} disabled={disabled} open={open} onOpenChange={value => { onOpenChange(value); if (value) setQuery(""); }}>
    {loading ? <p className="wpb-empty" role="status">Loading repositories…</p>
      : needsConnection && !repositories.length ? <div className="wst-picker-empty">
        <p>{status === "reconnect_required" ? "Your GitHub authorization expired. Reconnect to check out repositories." : "Connect GitHub to check out repositories in this Work. You can start without one."}</p>
        <Button size="sm" variant="secondary" isPending={connect.isPending} onPress={() => connect.mutate({ provider: "github", returnTo })}>{status === "reconnect_required" ? "Reconnect GitHub" : `Connect ${providerName("github")}`}</Button>
      </div>
      : unavailable ? <p className="wpb-empty" role="alert">Repositories couldn’t be loaded. You can start without one. <Button size="sm" variant="ghost" onPress={onRetry}>Retry</Button></p>
      : <>
        <Search label="Search repositories" value={query} onChange={setQuery} />
        <div className="wst-scroll">
          {!repositories.length ? <p className="wpb-empty" role="status">GitHub doesn’t allow Talgent into any repository yet.</p>
            : query ? matches.length ? <div className="wst-rows">{matches.map(row)}</div> : <p className="wpb-empty" role="status">No repositories match.</p>
            : <>{group("Recent", recentRows)}{group(recentRows.length ? "All repositories" : "Repositories", repositories.filter(repository => !recent.includes(repository.id)))}</>}
        </div>
        <ScopeNote />
      </>}
  </TrayPopover>;
}

// One Startup Agent from the Plugins included in this Work.
export function AgentPicker({ agents, value, disabled, open, onOpenChange, onChange }: { agents: Capability[]; value: string; disabled: boolean; open: boolean; onOpenChange: (open: boolean) => void; onChange: (agent: string) => void }) {
  return <TrayPopover label="Startup Agent" value={value ? value.split(":").at(-1)! : "Default Agent"} icon={<Bot size={16} />} disabled={disabled} open={open} onOpenChange={onOpenChange}>
    <ListBox aria-label="Startup Agent" selectionMode="single" selectedKeys={[value || "default"]} onSelectionChange={keys => { if (keys === "all" || !keys.size) return; const next = String([...keys][0]); onChange(next === "default" ? "" : next); onOpenChange(false); }}>
      <ListBox.Item id="default" textValue="Default Agent" className="wpb-choice"><Bot size={16} /><span className="wpb-choice-copy"><Label>Default Agent</Label><span className="wpb-muted">Talgent</span></span><ListBox.ItemIndicator /></ListBox.Item>
      {agents.map(agent => <ListBox.Item key={agent.name} id={agent.name} textValue={agent.name} className="wpb-choice"><Bot size={16} /><span className="wpb-choice-copy"><Label title={agent.description}>{agent.name.split(":").at(-1)}</Label><span className="wpb-muted">{agent.plugin}</span></span><ListBox.ItemIndicator /></ListBox.Item>)}
    </ListBox>
  </TrayPopover>;
}

type Harness = NonNullable<WorkStartInput["harness"]>;

// How the Harness runs on a Managed Computer: its permission mode, and whether
// it runs in Claude Code's sandbox. A Cloud has neither, so it is never offered.
// A machine that reports no sandbox can't be asked for one.
export function HarnessPicker({ value, sandboxAvailable, disabled, open, onOpenChange, onChange }: { value: Harness; sandboxAvailable: boolean; disabled: boolean; open: boolean; onOpenChange: (open: boolean) => void; onChange: (value: Harness) => void }) {
  const mode = permissionModeOption(value.permissionMode);
  return <TrayPopover label="Permissions" value={`${mode?.short ?? "Permissions"}${value.sandbox ? " · Sandbox" : ""}`} icon={<Shield size={16} />} disabled={disabled} open={open} onOpenChange={onOpenChange}>
    <PermissionModeList value={value.permissionMode} onChange={permissionMode => onChange({ ...value, permissionMode })} />
    <div className="wst-switch-row">
      <Switch size="sm" isSelected={value.sandbox} isDisabled={!sandboxAvailable} onChange={sandbox => onChange({ ...value, sandbox })}>
        <Switch.Content>Run in Claude Code’s sandbox<Switch.Control><Switch.Thumb /></Switch.Control></Switch.Content>
        {!sandboxAvailable && <Description className="wst-switch-note">This Computer can’t provide it</Description>}
      </Switch>
    </div>
    <ScopeNote />
  </TrayPopover>;
}
