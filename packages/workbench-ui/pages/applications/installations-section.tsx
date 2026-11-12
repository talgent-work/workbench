import { useState, type ReactNode } from "react";
import { Button, Card, Chip, Switch, Tooltip } from "@heroui/react";
import type { Connector, InstalledPlugin, PluginSummary } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";
import { AlertTriangle, Plus, Settings2, Trash2 } from "lucide-react";
import type { JsonObject } from "@bufbuild/protobuf";
import { ConnectorIcon, PluginIcon } from "../../components/marketplace/marketplace-icons";
import { ConfirmDialog, LoadState } from "../../components/marketplace/marketplace-shared";
import type { Application } from "../../lib/application-service";
import { useConnectorActions, useConnectors, useInstalledConnectors, useInstalledPlugins, usePluginActions, usePluginCatalog } from "../../lib/marketplace-catalog";
import { providerName, serviceLabel } from "../../lib/marketplace-connections";
import { capabilityText, pluginDisplayName, pluginPresentation } from "../../lib/marketplace-plugins";
import { installationNeedsSetup } from "../../lib/plugin-parameters";
import { notify } from "../../lib/notifications";
import { CatalogDialog, type CatalogKind } from "./catalog-dialog";
import { PluginSettingsDialog } from "./plugin-settings-dialog";

type Settings = { plugin: PluginSummary; installed?: InstalledPlugin };
type Removal = { kind: "plugin"; installed: InstalledPlugin; name: string } | { kind: "connector"; connector: Connector; name: string };

/** The Plugins and Connectors installed for the Application. Enabled ones load
 * in every Work it starts unless its start request leaves them out; the
 * Marketplace catalog adds more, installing for the Application, never for
 * the person managing it. required are the Plugins every Work loads anyway. */
export function InstallationsSection({ application, required }: { application: Application; required: Set<string> }) {
  const owner = { applicationId: application.id };
  const catalog = usePluginCatalog();
  const installedPlugins = useInstalledPlugins(owner);
  const installedConnectors = useInstalledConnectors(owner);
  const [adding, setAdding] = useState<CatalogKind | null>(null);
  const connectorCatalog = useConnectors(owner);
  const pluginActions = usePluginActions(owner);
  const connectorActions = useConnectorActions(owner);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [removal, setRemoval] = useState<Removal | null>(null);
  const summaries = new Map((catalog.data ?? []).map(plugin => [plugin.slug, plugin]));
  const plugins = [...(installedPlugins.data?.values() ?? [])].filter(item => !required.has(item.slug));
  const connectors = (installedConnectors.data ?? []).filter(item => !item.required);
  const failed = (subject: string) => notify(`application:${application.id}:${subject}`, "danger", `${subject} couldn’t be changed`, "Refresh and try again.");

  const installPlugin = (plugin: PluginSummary, configuration: JsonObject = {}) => {
    const name = pluginPresentation(plugin).name;
    pluginActions.install.mutate({ slug: plugin.slug, configuration }, {
      onSuccess: () => { setSettings(null); notify(`application:${application.id}:${plugin.slug}`, "success", `${name} installed for ${application.name}`, "Works it starts from now on load it."); },
      onError: () => notify(`application:${application.id}:${plugin.slug}`, "danger", `${name} couldn’t be installed`, "Check its settings and try again."),
    });
  };
  const saveSettings = (values: JsonObject, cleared: string[]) => {
    if (!settings) return;
    if (!settings.installed) { installPlugin(settings.plugin, values); return; }
    pluginActions.configure.mutate({ installed: settings.installed, values, cleared }, { onSuccess: () => setSettings(null) });
  };
  const remove = () => {
    if (!removal) return;
    if (removal.kind === "plugin") pluginActions.uninstall.mutate(removal.installed, { onError: () => failed(removal.name) });
    else connectorActions.uninstall.mutate(removal.connector, { onError: () => failed(removal.name) });
  };

  return <>
    <Group id="ap-plugins" title="Plugins" count={plugins.length} onAdd={() => setAdding("plugins")}>
      <p className="mk-note">Enabled Plugins load in every Work {application.name} starts. A start request can add others or leave these out.</p>
      <LoadState noun="Plugins" isPending={installedPlugins.isPending} isError={installedPlugins.isError} hasData={Boolean(installedPlugins.data)} isFetching={installedPlugins.isFetching} onRetry={() => void installedPlugins.refetch()} />
      {installedPlugins.data && (plugins.length === 0
        ? <Card className="mk-group"><div className="mk-empty"><strong>No Plugins yet</strong><span className="text-muted">Add Skills and Agents from the Marketplace for this Agent to use.</span></div></Card>
        : <Card className="mk-group"><ul className="mk-group-list" aria-label={`${application.name} Plugins`}>
          {plugins.map(item => {
            const summary = summaries.get(item.slug);
            const name = summary ? pluginPresentation(summary).name : pluginDisplayName(item.slug);
            const setup = summary ? installationNeedsSetup(summary.parameters, item) : false;
            return <li key={item.slug} className="!items-center">
              {summary ? <PluginIcon plugin={summary} size={36} /> : <span className="size-9" aria-hidden="true" />}
              <span className="mk-row-text min-w-0 flex-1">
                <strong className={item.enabled ? undefined : "text-muted"}>{name}{setup && <Warning text="Needs setup" />}</strong>
                {summary && <span>{capabilityText(summary)}</span>}
              </span>
              {summary && Object.keys(summary.parameters).length > 0 && <IconButton label={`Configure ${name}`} onPress={() => setSettings({ plugin: summary, installed: item })}><Settings2 size={15} aria-hidden="true" /></IconButton>}
              <IconButton label={`Remove ${name}`} onPress={() => setRemoval({ kind: "plugin", installed: item, name })}><Trash2 size={15} aria-hidden="true" /></IconButton>
              <Toggle label={`Load ${name} by default`} isSelected={item.enabled} isDisabled={pluginActions.setEnabled.isPending}
                onChange={enabled => pluginActions.setEnabled.mutate({ installed: item, enabled }, { onError: () => failed(name) })} />
            </li>;
          })}
        </ul></Card>)}
    </Group>
    <Group id="ap-connectors" title="Connectors" count={connectors.length} onAdd={() => setAdding("connectors")}>
      <LoadState noun="Connectors" isPending={installedConnectors.isPending} isError={installedConnectors.isError} hasData={Boolean(installedConnectors.data)} isFetching={installedConnectors.isFetching} onRetry={() => void installedConnectors.refetch()} />
      {installedConnectors.data && (connectors.length === 0 ? <p className="mk-note">No Connectors yet.</p>
        : <Card className="mk-group"><ul className="mk-group-list" aria-label={`${application.name} Connectors`}>
          {connectors.map(item => {
            const name = serviceLabel(item.name);
            const enabled = item.status === "enabled";
            const missing = Boolean(item.provider) && item.providerConnection !== "connected";
            return <li key={item.name} className="!items-center">
              <ConnectorIcon name={item.name} size={36} />
              <span className="mk-row-text min-w-0 flex-1">
                <strong className={enabled ? undefined : "text-muted"}>{name}{missing && <Warning text={`${providerName(item.provider)} not connected`} />}</strong>
                <span>{missing ? `Connect ${providerName(item.provider)} for ${application.name} so its Works can use it.` : item.description}</span>
              </span>
              <IconButton label={`Remove ${name}`} onPress={() => setRemoval({ kind: "connector", connector: item, name })}><Trash2 size={15} aria-hidden="true" /></IconButton>
              <Toggle label={`Load ${name} by default`} isSelected={enabled} isDisabled={connectorActions.setEnabled.isPending}
                onChange={value => connectorActions.setEnabled.mutate({ connector: item, enabled: value }, { onError: () => failed(name) })} />
            </li>;
          })}
        </ul></Card>)}
    </Group>
    {adding && <CatalogDialog applicationName={application.name} initial={adding} plugins={catalog} connectors={connectorCatalog}
      installedPlugins={installedPlugins.data ?? new Map()} required={required}
      installing={pluginActions.install.isPending ? `plugin:${pluginActions.install.variables?.slug}` : connectorActions.install.isPending ? `connector:${connectorActions.install.variables}` : ""}
      onInstallPlugin={(plugin, needsSettings) => needsSettings ? setSettings({ plugin }) : installPlugin(plugin)}
      onInstallConnector={connector => connectorActions.install.mutate(connector.name, { onError: () => failed(serviceLabel(connector.name)) })}
      onClose={() => setAdding(null)} />}
    {settings && <PluginSettingsDialog plugin={settings.plugin} installed={settings.installed} saving={pluginActions.install.isPending || pluginActions.configure.isPending}
      error={pluginActions.configure.isError ? "The settings couldn’t be saved. Check the values and try again." : pluginActions.install.isError ? "It couldn’t be installed. Check the values and try again." : ""}
      onSave={saveSettings} onClose={() => { pluginActions.install.reset(); pluginActions.configure.reset(); setSettings(null); }} />}
    <ConfirmDialog isOpen={Boolean(removal)} onOpenChange={open => { if (!open) setRemoval(null); }} title={`Remove ${removal?.name ?? ""} from ${application.name}?`} confirmLabel="Remove" onConfirm={remove}>
      <p>{removal?.kind === "plugin" ? "Its saved settings are removed. " : ""}Works already running keep it; Works {application.name} starts from now on don’t load it.</p>
    </ConfirmDialog>
  </>;
}

function Group({ id, title, count, onAdd, children }: { id: string; title: string; count: number; onAdd: () => void; children: ReactNode }) {
  return <section className="mk-section" aria-labelledby={id}>
    <div className="mk-section-bar"><h3 id={id} className="mk-section-title">{title} <span className="mk-count">{count}</span></h3>
      <Button size="sm" variant="ghost" aria-label={`Add ${title}`} onPress={onAdd}><Plus size={15} aria-hidden="true" />Add</Button></div>
    {children}
  </section>;
}

function Toggle({ label, isSelected, isDisabled, onChange }: { label: string; isSelected: boolean; isDisabled: boolean; onChange: (value: boolean) => void }) {
  return <Tooltip delay={300}>
    <Switch size="sm" aria-label={label} isSelected={isSelected} isDisabled={isDisabled} onChange={onChange}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control></Switch.Content></Switch>
    <Tooltip.Content>{isSelected ? "Loads in every Work" : "Installed, not loaded by default"}</Tooltip.Content>
  </Tooltip>;
}

function Warning({ text }: { text: string }) {
  return <Chip size="sm" variant="soft" color="warning" className="ml-2"><AlertTriangle size={12} aria-hidden="true" /><Chip.Label>{text}</Chip.Label></Chip>;
}

function IconButton({ label, onPress, children }: { label: string; onPress: () => void; children: ReactNode }) {
  return <Tooltip delay={300}><Button size="sm" variant="ghost" isIconOnly aria-label={label} onPress={onPress}>{children}</Button><Tooltip.Content>{label}</Tooltip.Content></Tooltip>;
}
