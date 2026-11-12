// PROTOTYPE (Applications): what an Application's Agent loads. The owner installs
// Plugins and Connectors for the Application from the Marketplace catalog;
// enabled ones load in every Work it starts unless the start request leaves
// them out. Provider Authorizations are the Application's own.
import { useState } from "react";
import { Alert, Button, Card, Chip, FieldError, Input, Label, Modal, SearchField, Switch, Tabs, TextField, Tooltip } from "@heroui/react";
import { AlertTriangle, Check, Plus, Settings2, Trash2 } from "lucide-react";
import { ConnectorIcon, PluginIcon, ProviderIcon } from "../../../components/marketplace/marketplace-icons";
import { catalogConnectors, catalogPlugins, capabilityText, connectorOf, needsSetup, pluginOf, presentable, providerLabels, type InstalledPlugin } from "./proto-catalog";
import { useProtoStore, type ProtoApplication } from "./proto-data";
import { Section } from "./proto-parts";

type Kind = "plugins" | "connectors";

function Toggle({ label, isSelected, onChange }: { label: string; isSelected: boolean; onChange: (value: boolean) => void }) {
  return <Tooltip delay={300}>
    <Switch size="sm" aria-label={label} isSelected={isSelected} onChange={onChange}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control></Switch.Content></Switch>
    <Tooltip.Content>{isSelected ? "Loads in every Work" : "Installed, not loaded by default"}</Tooltip.Content>
  </Tooltip>;
}

function Warning({ compact, text }: { compact: boolean; text: string }) {
  if (!compact) return <Chip size="sm" variant="soft" color="warning"><AlertTriangle size={12} aria-hidden="true" /><Chip.Label>{text}</Chip.Label></Chip>;
  return <Tooltip delay={200}><Tooltip.Trigger aria-label={text} className="text-warning"><AlertTriangle size={14} aria-hidden="true" /></Tooltip.Trigger><Tooltip.Content>{text}</Tooltip.Content></Tooltip>;
}

function IconButton({ label, onPress, children }: { label: string; onPress: () => void; children: React.ReactNode }) {
  return <Tooltip delay={300}><Button size="sm" variant="ghost" isIconOnly aria-label={label} onPress={onPress}>{children}</Button><Tooltip.Content>{label}</Tooltip.Content></Tooltip>;
}

/** Plugins, Connectors and Providers of one Application; compact drops the summaries for a side column. */
export function Loadout({ application, compact = false }: { application: ProtoApplication; compact?: boolean }) {
  const store = useProtoStore();
  const [adding, setAdding] = useState<Kind | null>(null);
  const [configuring, setConfiguring] = useState<InstalledPlugin | null>(null);
  const plugins = application.plugins;
  const connectors = application.connectors;
  const setPlugin = (slug: string, change: Partial<InstalledPlugin> | null) => store.setPlugins(application.id, list => change ? list.map(item => item.slug === slug ? { ...item, ...change } : item) : list.filter(item => item.slug !== slug));
  const setConnector = (name: string, enabled: boolean | null) => store.setConnectors(application.id, list => enabled === null ? list.filter(item => item.name !== name) : list.map(item => item.name === name ? { ...item, enabled } : item));
  const add = (kind: Kind) => <Button size="sm" variant="ghost" onPress={() => setAdding(kind)}><Plus size={15} aria-hidden="true" />Add</Button>;
  const providerIds = [...new Set(["github", ...connectors.map(item => connectorOf(item.name).provider).filter((id): id is string => Boolean(id))])];
  return <div className="grid gap-8">
    <Section id={`lo-plugins-${compact}`} title="Plugins" count={plugins.length} action={add("plugins")}>
      {!compact && <p className="mk-note">Enabled Plugins load in every Work {application.name} starts. A start request can add others or leave these out.</p>}
      {plugins.length === 0 ? <Card className="mk-group"><div className="mk-empty"><strong>No Plugins yet</strong><span className="text-muted">Add Skills and Agents from the Marketplace for this Agent to use.</span></div></Card>
        : <Card className="mk-group"><ul className="mk-group-list" aria-label={`${application.name} Plugins`}>
          {plugins.map(item => {
            const plugin = pluginOf(item.slug);
            const setup = needsSetup(item);
            return <li key={item.slug} className="!items-center" data-off={!item.enabled || undefined}>
              <PluginIcon plugin={presentable(plugin)} size={compact ? 28 : 36} />
              <span className="mk-row-text min-w-0 flex-1">
                <strong className={item.enabled ? undefined : "text-muted"}>{plugin.name}{setup && <Warning compact={compact} text="Needs setup" />}</strong>
                {!compact && <span>{capabilityText(plugin)}</span>}
              </span>
              {plugin.parameter && <IconButton label={`Configure ${plugin.name}`} onPress={() => setConfiguring(item)}><Settings2 size={15} aria-hidden="true" /></IconButton>}
              {!compact && <IconButton label={`Remove ${plugin.name}`} onPress={() => setPlugin(item.slug, null)}><Trash2 size={15} aria-hidden="true" /></IconButton>}
              <Toggle label={`Load ${plugin.name} by default`} isSelected={item.enabled} onChange={enabled => setPlugin(item.slug, { enabled })} />
            </li>;
          })}
        </ul></Card>}
    </Section>
    <Section id={`lo-connectors-${compact}`} title="Connectors" count={connectors.length} action={add("connectors")}>
      {connectors.length === 0 ? <p className="mk-note">No Connectors yet.</p>
        : <Card className="mk-group"><ul className="mk-group-list" aria-label={`${application.name} Connectors`}>
          {connectors.map(item => {
            const connector = connectorOf(item.name);
            const missing = connector.provider && !application.providers[connector.provider];
            return <li key={item.name} className="!items-center">
              <ConnectorIcon name={connector.name} size={compact ? 28 : 36} />
              <span className="mk-row-text min-w-0 flex-1">
                <strong className={item.enabled ? undefined : "text-muted"}>{connector.label}{missing && <Warning compact={compact} text={`${providerLabels[connector.provider!]} not connected`} />}</strong>
                {!compact && <span>{missing ? `Connect ${providerLabels[connector.provider!]} below so Works can use it.` : connector.summary}</span>}
              </span>
              {!compact && <IconButton label={`Remove ${connector.label}`} onPress={() => setConnector(item.name, null)}><Trash2 size={15} aria-hidden="true" /></IconButton>}
              <Toggle label={`Load ${connector.label} by default`} isSelected={item.enabled} onChange={enabled => setConnector(item.name, enabled)} />
            </li>;
          })}
        </ul></Card>}
    </Section>
    <Section id={`lo-providers-${compact}`} title="Providers">
      {!compact && <p className="mk-note">Accounts {application.name}’s Works act with, such as the GitHub repositories they check out. Connected for the application, never for you.</p>}
      <Card className="mk-group"><ul className="mk-group-list" aria-label={`${application.name} Providers`}>
        {providerIds.map(id => {
          const account = application.providers[id];
          return <li key={id} className="!items-center">
            <ProviderIcon id={id} size={compact ? 28 : 36} />
            <span className="mk-row-text min-w-0 flex-1"><strong>{providerLabels[id]}</strong><span className="truncate">{account ? compact ? "Connected" : `Connected as ${account}` : "Not connected"}</span></span>
            <Button size="sm" variant={account ? "ghost" : "secondary"} onPress={() => store.setProvider(application.id, id, account ? null : `${application.name.toLowerCase().replace(/\W+/g, "-")}-bot`)}>{account ? "Disconnect" : "Connect"}</Button>
          </li>;
        })}
      </ul></Card>
    </Section>
    {adding && <CatalogDialog application={application} initial={adding} onClose={() => setAdding(null)} />}
    {configuring && <ConfigureDialog application={application} installed={configuring} onClose={() => setConfiguring(null)} />}
  </div>;
}

/** The Marketplace catalog, installing for the Application rather than for the person. */
function CatalogDialog({ application, initial, onClose }: { application: ProtoApplication; initial: Kind; onClose: () => void }) {
  const store = useProtoStore();
  const [kind, setKind] = useState<Kind>(initial);
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const plugins = catalogPlugins.filter(plugin => `${plugin.name} ${plugin.summary}`.toLowerCase().includes(q));
  const connectors = catalogConnectors.filter(connector => `${connector.label} ${connector.summary}`.toLowerCase().includes(q));
  const installedPlugin = (slug: string) => application.plugins.some(item => item.slug === slug);
  const installedConnector = (name: string) => application.connectors.some(item => item.name === name);
  const install = (label: string, installed: boolean, onInstall: () => void) => installed
    ? <span className="inline-flex items-center gap-1 px-2 text-[13px] text-muted"><Check size={14} aria-hidden="true" />Installed</span>
    : <Button size="sm" variant="secondary" aria-label={`Install ${label}`} onPress={onInstall}>Install</Button>;
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open) onClose(); }}><Modal.Container placement="center" size="lg" scroll="inside"><Modal.Dialog>
    <Modal.Header><Modal.Heading>Add to {application.name}</Modal.Heading></Modal.Header>
    <Modal.Body className="grid gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <Tabs selectedKey={kind} onSelectionChange={key => setKind(key as Kind)}>
          <Tabs.ListContainer><Tabs.List aria-label="Catalog">{(["plugins", "connectors"] as const).map(id => <Tabs.Tab key={id} id={id}>{id === "plugins" ? "Plugins" : "Connectors"}<Tabs.Indicator /></Tabs.Tab>)}</Tabs.List></Tabs.ListContainer>
        </Tabs>
        <SearchField aria-label="Search the catalog" value={query} onChange={setQuery} className="min-w-48 flex-1">
          <SearchField.Group><SearchField.SearchIcon /><SearchField.Input placeholder={`Search ${kind}`} /><SearchField.ClearButton aria-label="Clear search" /></SearchField.Group>
        </SearchField>
      </div>
      <ul className="mk-group-list" aria-label={`Catalog ${kind}`}>
        {kind === "plugins" ? plugins.map(plugin => <li key={plugin.slug} className="!items-center">
          <PluginIcon plugin={presentable(plugin)} size={36} />
          <span className="mk-row-text min-w-0 flex-1"><strong>{plugin.name}</strong><span>{plugin.summary}</span><span>{capabilityText(plugin)}{plugin.parameter && " · needs setup"}</span></span>
          {install(plugin.name, installedPlugin(plugin.slug), () => store.setPlugins(application.id, list => [...list, { slug: plugin.slug, enabled: true }]))}
        </li>) : connectors.map(connector => <li key={connector.name} className="!items-center">
          <ConnectorIcon name={connector.name} size={36} />
          <span className="mk-row-text min-w-0 flex-1"><strong>{connector.label}</strong><span>{connector.summary}</span></span>
          {install(connector.label, installedConnector(connector.name), () => store.setConnectors(application.id, list => [...list, { name: connector.name, enabled: true }]))}
        </li>)}
        {(kind === "plugins" ? plugins : connectors).length === 0 && <li><span className="text-[13px] text-muted">Nothing matches “{query}”.</span></li>}
      </ul>
    </Modal.Body>
    <Modal.Footer><Button onPress={onClose}>Done</Button></Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}

function ConfigureDialog({ application, installed, onClose }: { application: ProtoApplication; installed: InstalledPlugin; onClose: () => void }) {
  const store = useProtoStore();
  const plugin = pluginOf(installed.slug);
  const parameter = plugin.parameter!;
  const [value, setValue] = useState(installed.value ?? "");
  const problem = value && !/^https:\/\//.test(value) ? "Use an https:// address." : "";
  const save = () => { if (!value || problem) return; store.setPlugins(application.id, list => list.map(item => item.slug === installed.slug ? { ...item, value } : item)); onClose(); };
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open) onClose(); }}><Modal.Container placement="center" size="sm"><Modal.Dialog>
    <Modal.Header><Modal.Heading>Set up {plugin.name}</Modal.Heading></Modal.Header>
    <Modal.Body className="grid gap-3">
      <TextField value={value} onChange={setValue} isInvalid={Boolean(problem)} autoFocus><Label>{parameter.label}</Label><Input placeholder="https://" onKeyDown={event => { if (event.key === "Enter") save(); }} /><FieldError>{problem}</FieldError></TextField>
      <p className="text-[13px] text-muted">{parameter.description}</p>
      {!installed.value && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Description>Until it’s set up, Works load {plugin.name} without its documentation search.</Alert.Description></Alert.Content></Alert>}
    </Modal.Body>
    <Modal.Footer><Button variant="secondary" onPress={onClose}>Cancel</Button><Button isDisabled={!value || Boolean(problem)} onPress={save}>Save</Button></Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}
