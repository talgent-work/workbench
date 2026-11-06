import { useState, type ReactNode } from "react";
import { Button, ListBox } from "@heroui/react";
import type { PluginSummary } from "@talgent/workbench-contracts/marketplace/public/v1/marketplace_pb";
import { AlertTriangle, ArrowLeft, Plug, Puzzle, Settings2 } from "lucide-react";
import { providerName, serviceLabel, useConnectProvider } from "../../lib/marketplace-connections";
import { pluginDisplayName, pluginPresentation } from "../../lib/marketplace-plugins";
import { draftsFrom } from "../../lib/plugin-parameters";
import type { ConnectorRow, LoadoutRow } from "../../lib/work-start-loadout";
import type { WorkStartDraft } from "../../lib/work-start-draft";
import { ParameterFields } from "../marketplace/parameter-fields";
import { CheckRow, ScopeNote, Search, TrayPopover } from "./work-start-tray";

type Update = (change: Partial<WorkStartDraft> | ((draft: WorkStartDraft) => WorkStartDraft)) => void;
type Entry = { key: string; category: string; installed: boolean; title: string; secondary: string; warn?: boolean; checked: boolean; disabled?: boolean; trailing?: ReactNode; onChange: () => void };

const installedSection = "installed";
const byCategory = (a: string, b: string) => a === "Other" ? 1 : b === "Other" ? -1 : a.localeCompare(b);

// Plugins and Connectors browse by catalog category. The rail opens on
// Installed — the starter's installations and required entries — then every
// category; checking an entry anywhere includes it in this Work, so browsing
// and adding are one list. A search spans every category.
function CatalogPicker({ noun, entries }: { noun: string; entries: Entry[] }) {
  const [section, setSection] = useState(installedSection);
  const [query, setQuery] = useState("");
  const categories = [...new Set(entries.map(entry => entry.category))].sort(byCategory);
  const searching = Boolean(query.trim());
  const shown = searching ? entries.filter(entry => `${entry.title} ${entry.secondary} ${entry.category}`.toLowerCase().includes(query.trim().toLowerCase()))
    : entries.filter(entry => section === installedSection ? entry.installed : entry.category === section);
  const count = (predicate: (entry: Entry) => boolean) => entries.filter(predicate).length;
  return <>
    <Search label={`Search ${noun}`} value={query} onChange={setQuery} />
    <div className="wst-catalog" data-searching={searching || undefined}>
      {!searching && <ListBox aria-label={`${noun} categories`} selectionMode="single" disallowEmptySelection selectedKeys={[section]} className="wst-rail"
        onSelectionChange={keys => { if (keys !== "all") { const next = [...keys][0]; if (next !== undefined) setSection(String(next)); } }}>
        {[[installedSection, "Installed", count(entry => entry.installed)] as const, ...categories.map(name => [name, name, count(entry => entry.category === name)] as const)].map(([id, name, total]) =>
          <ListBox.Item key={id} id={id} textValue={name} className="wst-rail-item"><span className="wst-rail-name">{name}</span><span className="wpb-muted wst-count">{total}</span></ListBox.Item>)}
      </ListBox>}
      <div className="wst-rows wst-pane" role="group" aria-label={searching ? `Matching ${noun}` : section === installedSection ? "Installed" : section}>
        {shown.length ? shown.map(entry => <CheckRow key={entry.key} label={`Include ${entry.title} in this Work`} checked={entry.checked} disabled={entry.disabled} struck={entry.installed && !entry.checked} title={entry.title}
          secondary={searching ? `${entry.category} · ${entry.secondary}` : entry.secondary} warn={entry.warn} trailing={entry.trailing} onChange={entry.onChange} />)
          : <p className="wpb-empty" role="status">{searching ? `No ${noun} match.` : `No ${noun} here yet.`}</p>}
      </div>
    </div>
    <ScopeNote />
  </>;
}

const summary = (row: LoadoutRow) => [row.skills.length ? `${row.skills.length} ${row.skills.length === 1 ? "skill" : "skills"}` : "", row.agents.length ? `${row.agents.length} ${row.agents.length === 1 ? "agent" : "agents"}` : ""].filter(Boolean).join(" · ") || "No skills or agents";
const rowState = (row: LoadoutRow) => row.problem || (row.difference === "removed" ? "Off for this Work" : row.edited.length ? `${row.edited.length} ${row.edited.length === 1 ? "setting" : "settings"} changed for this Work` : row.difference === "required" ? "Always included" : summary(row));
const hasSettings = (release?: PluginSummary) => Boolean(release && Object.keys(release.parameters).length);

// Plugins of this Work as differences from the defaults. `view` is a Plugin's
// slug while its settings for this Work are open.
export function PluginsMenu({ rows, catalog, draft, update, disabled, open, onOpenChange, view, onViewChange, errors }: {
  rows: LoadoutRow[]; catalog: PluginSummary[]; draft: WorkStartDraft; update: Update; disabled: boolean; open: boolean; onOpenChange: (open: boolean) => void;
  view: string; onViewChange: (view: string) => void; errors: Record<string, Record<string, string>>;
}) {
  const included = rows.filter(row => row.included);
  const changes = rows.filter(row => row.difference === "added" || row.difference === "removed" || row.edited.length).length;
  const toggle = (row: LoadoutRow) => update(current => {
    const plugins = { ...current.plugins };
    if (row.difference === "added") delete plugins[row.slug];
    else if (row.included) plugins[row.slug] = { include: false, values: plugins[row.slug]?.values ?? {} };
    else plugins[row.slug] = { values: plugins[row.slug]?.values ?? {} };
    // A Startup Agent provided only by a removed Plugin resets to the default.
    const agent = row.included && row.agents.some(item => item.name === current.agent) ? "" : current.agent;
    return { ...current, plugins, agent };
  });
  const add = (slug: string) => update(current => ({ ...current, plugins: { ...current.plugins, [slug]: { include: true, values: {} } } }));
  const detail = rows.find(row => row.slug === view);
  const entries: Entry[] = [
    ...rows.map(row => ({ key: row.slug, category: row.release ? pluginPresentation(row.release).category : "Other", installed: row.difference !== "added", title: row.name, secondary: rowState(row), warn: Boolean(row.problem),
      checked: row.included, disabled: row.difference === "required", onChange: () => toggle(row),
      trailing: row.included && row.difference !== "required" && hasSettings(row.release) ? <Button size="sm" variant="ghost" isIconOnly aria-label={`${row.name} settings for this Work`} onPress={() => onViewChange(row.slug)}>{row.problem ? <AlertTriangle size={14} className="text-warning" aria-hidden="true" /> : <Settings2 size={14} aria-hidden="true" />}</Button> : undefined })),
    ...catalog.filter(release => !rows.some(row => row.slug === release.slug)).map(release => ({ key: release.slug, category: pluginPresentation(release).category, installed: false, title: pluginPresentation(release).name || pluginDisplayName(release.slug), secondary: release.description, checked: false, onChange: () => add(release.slug) })),
  ];
  return <TrayPopover label="Plugins" value={`${included.length} plugins${changes ? ` · ${changes} changed` : ""}`} icon={<Puzzle size={16} />} disabled={disabled} attention={included.some(row => row.problem)} open={open} wide={!detail} onOpenChange={value => { onOpenChange(value); if (value) onViewChange(""); }}>
    {detail ? <>
      {/* The settings view replaces the button that opened it; focus moves here so it never falls out of the popover and closes it. */}
      <div className="wst-back-row"><Button size="sm" variant="ghost" isIconOnly autoFocus aria-label="Back to plugins" onPress={() => onViewChange("")}><ArrowLeft size={15} /></Button><span className="wst-heading">{detail.name}</span></div>
      {hasSettings(detail.release) ? <div className="wst-params">
        <ParameterFields declarations={detail.release!.parameters} drafts={draft.plugins[detail.slug]?.values ?? {}} errors={errors[detail.slug]} redacted={detail.installed?.redactedParameters} inherited={draftsFrom(detail.installed?.configuration)}
          onChange={(name, value) => update(current => ({ ...current, plugins: { ...current.plugins, [detail.slug]: { include: current.plugins[detail.slug]?.include, values: { ...(current.plugins[detail.slug]?.values ?? {}), [name]: value } } } }))} />
        {detail.edited.length > 0 && <Button size="sm" variant="ghost" onPress={() => update(current => ({ ...current, plugins: { ...current.plugins, [detail.slug]: { include: current.plugins[detail.slug]?.include, values: {} } } }))}>Use my defaults</Button>}
      </div> : <p className="wpb-empty">This plugin has no settings.</p>}
      <ScopeNote />
    </> : <CatalogPicker noun="plugins" entries={entries} />}
  </TrayPopover>;
}

// Connectors mirror Plugins: defaults on, removable or addable for this Work.
// A Provider that isn't connected is shown with a way to connect; it never
// blocks the start. A Connector a Work on the chosen Managed Computer leaves
// out is shown as unavailable, a required one included.
export function ConnectorsMenu({ rows, update, disabled, open, onOpenChange, returnTo }: { rows: ConnectorRow[]; update: Update; disabled: boolean; open: boolean; onOpenChange: (open: boolean) => void; returnTo: string }) {
  const connect = useConnectProvider();
  const included = rows.filter(row => row.included);
  const toggle = (row: ConnectorRow) => update(current => {
    const connectors = { ...current.connectors };
    if (row.difference === "removed" || (row.difference === "added" && row.included)) delete connectors[row.name];
    else connectors[row.name] = !row.included;
    return { ...current, connectors };
  });
  const entries: Entry[] = rows.map(row => {
    const provider = row.connector?.provider ?? "";
    const needs = Boolean(row.included && provider && row.connector?.providerConnection !== "connected");
    const category = row.connector?.category ? pluginDisplayName(row.connector.category) : "Other";
    return { key: row.name, category, installed: row.difference !== "added", title: serviceLabel(row.name), checked: row.included, disabled: row.difference === "required" || row.unavailable, warn: needs, onChange: () => toggle(row),
      secondary: row.unavailable ? "Not available on this Computer" : row.difference === "required" ? "Always included" : row.difference === "added" && row.included ? "Added for this Work" : needs ? `${providerName(provider)} isn’t connected — its tools won’t work` : provider ? `Signs in with ${providerName(provider)}` : row.connector?.description ?? "",
      trailing: needs ? <Button size="sm" variant="secondary" isPending={connect.isPending} onPress={() => connect.mutate({ provider, returnTo })}>Connect</Button> : undefined };
  });
  return <TrayPopover label="Connectors" value={`${included.length} ${included.length === 1 ? "connector" : "connectors"}`} icon={<Plug size={16} />} disabled={disabled} wide attention={included.some(row => row.connector?.provider && row.connector.providerConnection !== "connected")} open={open} onOpenChange={onOpenChange}>
    <CatalogPicker noun="connectors" entries={entries} />
  </TrayPopover>;
}
